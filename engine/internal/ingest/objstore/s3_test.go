package objstore

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// fakeS3 returns two pages, exercising the paginator: page 1 is truncated with
// a continuation token, page 2 finishes.
type fakeS3 struct{ calls int }

func (f *fakeS3) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	f.calls++
	if in.ContinuationToken == nil {
		return &s3.ListObjectsV2Output{
			Contents: []types.Object{
				{Size: aws.Int64(100), StorageClass: types.ObjectStorageClassStandard},
				{Size: aws.Int64(200), StorageClass: types.ObjectStorageClassGlacierIr},
			},
			IsTruncated:           aws.Bool(true),
			NextContinuationToken: aws.String("page2"),
		}, nil
	}
	return &s3.ListObjectsV2Output{
		Contents: []types.Object{
			{Size: aws.Int64(50), StorageClass: types.ObjectStorageClassGlacierIr},
		},
		IsTruncated: aws.Bool(false),
	}, nil
}

// EachObject streams across all pages; StorageBytesByClass then sums correctly.
func TestS3ListerPaginates(t *testing.T) {
	l := &S3Lister{api: &fakeS3{}, bucket: "argus-blocks"}
	src := StorageSource{Lister: l}

	got, err := src.StorageBytesByClass(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got["STANDARD"] != 100 {
		t.Errorf("STANDARD = %d, want 100", got["STANDARD"])
	}
	if got["GLACIER_IR"] != 250 { // 200 + 50 across two pages
		t.Errorf("GLACIER_IR = %d, want 250 (both pages)", got["GLACIER_IR"])
	}
}
