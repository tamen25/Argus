package objstore

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Config points the lister at a bucket. Endpoint/PathStyle support MinIO on
// the kind cluster; leave Endpoint empty for real AWS S3. Credentials come
// from the default AWS chain (env, shared config, IRSA) — read-only access is
// all that's needed.
type S3Config struct {
	Bucket    string
	Prefix    string // optional key prefix to scope the inventory
	Region    string
	Endpoint  string // e.g. http://minio.lgtm.svc:9000 (MinIO); empty for AWS
	PathStyle bool   // true for MinIO
}

// s3ListAPI is the slice of the S3 client this lister uses — narrowed so the
// pagination logic can be unit-tested with a fake (see s3_test.go).
type s3ListAPI interface {
	ListObjectsV2(ctx context.Context, in *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// S3Lister implements Lister over the S3 ListObjectsV2 API. Works against AWS
// S3 and MinIO (S3-compatible).
type S3Lister struct {
	api    s3ListAPI
	bucket string
	prefix string
}

// NewS3Lister builds an S3Lister from the default AWS config plus the given
// overrides.
func NewS3Lister(ctx context.Context, cfg S3Config) (*S3Lister, error) {
	loadOpts := []func(*awsconfig.LoadOptions) error{}
	if cfg.Region != "" {
		loadOpts = append(loadOpts, awsconfig.WithRegion(cfg.Region))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.PathStyle
	})
	return &S3Lister{api: client, bucket: cfg.Bucket, prefix: cfg.Prefix}, nil
}

// EachObject streams every object under the bucket/prefix, paginating so
// memory stays bounded regardless of object count.
func (l *S3Lister) EachObject(ctx context.Context, fn func(Object)) error {
	p := s3.NewListObjectsV2Paginator(l.api, &s3.ListObjectsV2Input{
		Bucket: aws.String(l.bucket),
		Prefix: nilIfEmpty(l.prefix),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, obj := range page.Contents {
			fn(Object{Size: derefInt64(obj.Size), StorageClass: string(obj.StorageClass)})
		}
	}
	return nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
