# consul-s3-snapshot

Create and restore consul snapshot with s3 and kms.

```
usage: consul-s3-snapshot --s3-bucket=S3-BUCKET --s3-region=S3-REGION [<flags>] <command> [<args> ...]

Save and restore consul snapshots to s3.

Flags:
  --help                   Show context-sensitive help (also try --help-long and --help-man).
  --s3-bucket=S3-BUCKET    S3 bucket name
  --s3-region=S3-REGION    S3 bucket region
  --kms-region=KMS-REGION  KMS region

Commands:
  help [<command>...]
    Show help.


  save --s3-prefix=S3-PREFIX [<flags>]
    Snapshot and upload to s3

    --s3-prefix=S3-PREFIX      S3 bucket prefix
    --kms-key-arn=KMS-KEY-ARN  KMS key arn

  restore --s3-path=S3-PATH
    Restore a snapshot from s3

    --s3-path=S3-PATH  S3 bucket path
```

## Usage

If you want to specify a specific AWS profile to use instead of your default one, prefix each of the command with `AWS_PROFILE=<profile-name>`

### Save

You need to specify the s3 bucket and its region as well as a prefix. The final filename will have the format
```
<prefix><last-index>-<time>.<extension>
```

* *prefix* is a s3 path, you can use `/` for folder and anything after the last `/` will prefix the filename
* *last-index* is the last index in the snapshot
* *time* has the format `HHHHMMDD-HHMMSS`
* *extension* will be `zip` for unencrypted snapshots and `enc` for encrypted ones


#### Without KMS

```
consul-s3-snapshot save --s3-bucket <bucket-name> \
                        --s3-region <bucket-region> \
                        --s3-prefix <path/to/prefix-blah>
```

**Example**

```
$ consul-s3-snapshot save --s3-bucket bucket-name \
                          --s3-region eu-west-1 \
                          --s3-prefix consul/snapshot-
KMS not enabled
Uploaded to bucket-name/consul/snapshot-1303-20171118-002418.zip
```

#### With KMS

You need to specify both `--kms-key-arn` and `--kms-region` to encrypt the snapshot

```
consul-s3-snapshot save --s3-bucket <bucket-name> \
                        --s3-region <bucket-region> \
                        --s3-prefix <path/to/prefix-blah> \
                        --kms-key-arn <key-arn> \
                        --kms-region <kms-region>
```

**Example**

```
$ consul-s3-snapshot save --kms-key-arn aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee \
                          --kms-region eu-west-1 \
                          --s3-bucket bucket-name \
                          --s3-region eu-west-1 \
                          --s3-prefix consul/snapshot-
KMS enabled, using aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee
Uploaded to bucket-name/consul/snapshot-1311-20171118-002627.enc

```

### restore

#### Without KMS

```
consul-s3-snapshot restore --s3-bucket <bucket-name> \
                           --s3-region <s3-region> \
                           --s3-path <path/to/prefix>
```

**Example**

```
$ consul-s3-snapshot restore --s3-bucket bucket-name \
                             --s3-region eu-west-1 \
                             --s3-path consul/snapshot-1303-20171118-002418.zip
Restored from consul/snapshot-1303-20171118-002418.zip
```

#### With KMS

You need to add `--kms-region` to the Command

```
consul-s3-snapshot restore --s3-bucket <bucket-name> \
                           --s3-region <s3-region> \
                           --s3-path <path/to/prefix> \
                           --kms-region <kms-region>
```

**Example**

```
$ consul-s3-snapshot restore --s3-bucket bucket-name \
                             --s3-region eu-west-1 \
                             --s3-path consul/snapshot-1311-20171118-002627.enc \
                             --kms-region eu-west-1
Restored from consul/snapshot-1311-20171118-002627.enc
```
