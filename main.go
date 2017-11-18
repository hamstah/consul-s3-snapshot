package main

import (
	"bytes"
	"crypto/rand"
	"encoding/gob"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/kms"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/hashicorp/consul/api"
	"golang.org/x/crypto/nacl/secretbox"
	kingpin "gopkg.in/alecthomas/kingpin.v2"
)

var (
	app       = kingpin.New("consul-s3-snapshot", "Save and restore consul snapshots to s3.")
	s3Bucket  = app.Flag("s3-bucket", "S3 bucket name").Required().String()
	s3Region  = app.Flag("s3-region", "S3 bucket region").Required().String()
	kmsRegion = app.Flag("kms-region", "KMS region").String()

	saveCommand = app.Command("save", "Snapshot and upload to s3")

	s3Prefix  = saveCommand.Flag("s3-prefix", "S3 bucket prefix").Required().String()
	kmsKeyArn = saveCommand.Flag("kms-key-arn", "KMS key arn").String()

	restoreCommand = app.Command("restore", "Restore a snapshot from s3")
	s3Path         = restoreCommand.Flag("s3-path", "S3 bucket path").Required().String()
)

func main() {

	switch kingpin.MustParse(app.Parse(os.Args[1:])) {
	case saveCommand.FullCommand():
		save(*s3Bucket, *s3Region, *s3Prefix, *kmsRegion, *kmsKeyArn)
	case restoreCommand.FullCommand():
		restore(*s3Bucket, *s3Region, *s3Path, *kmsRegion)
	}

}

func save(s3Bucket string, s3Region string, s3Prefix string, kmsRegion string, kmsKeyArn string) {
	toUpload, lastIndex := getConsulSnapshot()
	var fileType string

	now := time.Now()
	key := fmt.Sprintf("%s%d-%s", s3Prefix, lastIndex, now.Format("20060102-150405"))

	if kmsKeyArn != "" {
		if kmsRegion == "" {
			fmt.Fprintln(os.Stderr, "--kms-key-region required when using --kms-key-arn")
			os.Exit(1)
		}
		kmsClient := newKmsClient(kmsRegion)
		var err error
		toUpload, err = kmsEncrypt(kmsClient, kmsKeyArn, toUpload)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Failed to encrypt data", err)
			os.Exit(1)
		}
		fileType = "application/octet-stream"
		key = fmt.Sprintf("%s.enc", key)

		fmt.Println("KMS enabled, using", kmsKeyArn)
	} else {
		fileType = "application/gzip"
		key = fmt.Sprintf("%s.zip", key)

		fmt.Println("KMS not enabled")
	}

	s3Upload(s3Bucket, s3Region, key, toUpload, fileType)
	fmt.Println(fmt.Sprintf("Uploaded to %s/%s", s3Bucket, key))
}

func restore(s3Bucket string, s3Region string, s3Path string, kmsRegion string) {
	f := s3Download(s3Bucket, s3Region, s3Path)

	buf := new(bytes.Buffer)
	buf.ReadFrom(f)
	content := buf.Bytes()
	f.Close()
	os.Remove(f.Name())

	if strings.HasSuffix(s3Path, ".enc") {
		if kmsRegion == "" {
			fmt.Fprintln(os.Stderr, "Must specify --kms-region when restoring an encrypted backup")
			os.Exit(1)
		}
		kmsClient := newKmsClient(kmsRegion)
		var err error
		content, err = kmsDecrypt(kmsClient, content)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Failed to decrypt snapshot", err)
			os.Exit(1)
		}
	}
	restoreConsulSnapshot(content)
	fmt.Println("Restored from", s3Path)
}

func restoreConsulSnapshot(snapshotContent []byte) {
	client, err := api.NewClient(api.DefaultConfig())
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to get consul client", err)
		os.Exit(1)
	}
	snapshot := client.Snapshot()
	if err := snapshot.Restore(nil, bytes.NewReader(snapshotContent)); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to get consul client", err)
		os.Exit(1)
	}
}

func getConsulSnapshot() ([]byte, uint64) {
	client, err := api.NewClient(api.DefaultConfig())
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to get consul client", err)
		os.Exit(1)
	}

	snapshot := client.Snapshot()
	snap, qm, err := snapshot.Save(nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to get consul snapshot", err)
		os.Exit(1)
	}
	defer snap.Close()

	buf := new(bytes.Buffer)
	buf.ReadFrom(snap)

	return buf.Bytes(), qm.LastIndex
}

func newKmsClient(kmsRegion string) *kms.KMS {
	sess := session.Must(session.NewSession())
	return kms.New(sess, aws.NewConfig().WithRegion(kmsRegion))
}

const (
	keyLength   = 32
	nonceLength = 24
)

type payload struct {
	Key     []byte
	Nonce   *[nonceLength]byte
	Message []byte
}

func kmsEncrypt(kmsClient *kms.KMS, kmsKeyArn string, plaintext []byte) ([]byte, error) {
	keySpec := "AES_128"
	dataKeyInput := kms.GenerateDataKeyInput{KeyId: &kmsKeyArn, KeySpec: &keySpec}

	dataKeyOutput, err := kmsClient.GenerateDataKey(&dataKeyInput)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to get kms data key", err)
		os.Exit(1)
	}

	// Initialize payload
	p := &payload{
		Key:   dataKeyOutput.CiphertextBlob,
		Nonce: &[nonceLength]byte{},
	}

	// Set nonce
	if _, err = rand.Read(p.Nonce[:]); err != nil {
		return nil, err
	}

	// Create key
	key := &[keyLength]byte{}
	copy(key[:], dataKeyOutput.Plaintext)

	// Encrypt message
	p.Message = secretbox.Seal(p.Message, plaintext, p.Nonce, key)

	buf := &bytes.Buffer{}
	if err := gob.NewEncoder(buf).Encode(p); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func kmsDecrypt(kmsClient *kms.KMS, ciphertext []byte) ([]byte, error) {
	// Decode ciphertext with gob
	var p payload
	gob.NewDecoder(bytes.NewReader(ciphertext)).Decode(&p)

	dataKeyOutput, err := kmsClient.Decrypt(&kms.DecryptInput{
		CiphertextBlob: p.Key,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to get data key", err)
		os.Exit(1)
	}

	key := &[keyLength]byte{}
	copy(key[:], dataKeyOutput.Plaintext)

	// Decrypt message
	var plaintext []byte
	plaintext, ok := secretbox.Open(plaintext, p.Message, p.Nonce, key)
	if !ok {
		return nil, fmt.Errorf("Failed to open secretbox")
	}
	return plaintext, nil
}

func s3Download(s3Bucket string, s3Region string, s3Path string) *os.File {
	sess := session.Must(session.NewSession())
	s3Client := s3.New(sess, aws.NewConfig().WithRegion(s3Region))
	downloader := s3manager.NewDownloaderWithClient(s3Client)

	f, err := ioutil.TempFile(".", "snap-restore")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to create destination file", err)
		os.Exit(1)
	}

	_, err = downloader.Download(f, &s3.GetObjectInput{
		Bucket: aws.String(s3Bucket),
		Key:    aws.String(s3Path),
	})

	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to download from s3", err)
		os.Remove(f.Name())
		os.Exit(1)
	}
	f.Seek(0, 0)
	return f
}

func s3Upload(s3Bucket string, s3Region string, key string, content []byte, fileType string) {
	sess := session.Must(session.NewSession())
	s3Client := s3.New(sess, aws.NewConfig().WithRegion(s3Region))

	params := &s3.PutObjectInput{
		Bucket:        aws.String(s3Bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(content),
		ContentLength: aws.Int64(int64(len(content))),
		ContentType:   aws.String(fileType),
	}
	_, err := s3Client.PutObject(params)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to upload to s3", err)
		os.Exit(1)
	}
}
