package objstore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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

// NewS3Lister against an S3-compatible HTTP endpoint, as MinIO on the kind
// cluster: path-style addressing, the configured bucket and prefix, and the
// XML listing parsed into sizes and storage classes (a missing size is 0).
func TestNewS3ListerAgainstAnS3Endpoint(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>argus-blocks</Name><Prefix>tenant/</Prefix><KeyCount>2</KeyCount><MaxKeys>1000</MaxKeys>
  <IsTruncated>false</IsTruncated>
  <Contents><Key>tenant/a</Key><Size>42</Size><StorageClass>STANDARD</StorageClass></Contents>
  <Contents><Key>tenant/b</Key><StorageClass>GLACIER_IR</StorageClass></Contents>
</ListBucketResult>`))
	}))
	defer srv.Close()

	l, err := NewS3Lister(context.Background(), S3Config{
		Bucket: "argus-blocks", Prefix: "tenant/", Region: "us-east-1",
		Endpoint: srv.URL, PathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []Object
	if err := l.EachObject(context.Background(), func(o Object) { got = append(got, o) }); err != nil {
		t.Fatal(err)
	}

	if gotPath != "/argus-blocks" {
		t.Errorf("path = %q, want /argus-blocks (path-style)", gotPath)
	}
	if !strings.Contains(gotQuery, "list-type=2") || !strings.Contains(gotQuery, "prefix=tenant%2F") {
		t.Errorf("query = %q, want ListObjectsV2 with the prefix", gotQuery)
	}
	want := []Object{{Size: 42, StorageClass: "STANDARD"}, {Size: 0, StorageClass: "GLACIER_IR"}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("objects = %+v, want %+v", got, want)
	}
}
