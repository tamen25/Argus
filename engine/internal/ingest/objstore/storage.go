// Package objstore is the concrete adapter for object-storage inventory
// (S3/MinIO) behind the cost.StorageSource port (architecture rule 1).
// Read-only: it lists object metadata (size, storage class), never contents.
package objstore

import "context"

// Object is one stored object's cost-relevant metadata.
type Object struct {
	Size         int64
	StorageClass string
}

// Lister streams object metadata. Streaming (callback per object) keeps memory
// bounded over buckets with millions of objects — nothing is accumulated but
// the per-class byte totals.
type Lister interface {
	EachObject(ctx context.Context, fn func(Object)) error
}

// StorageSource adapts a Lister to cost.StorageSource: bytes summed by storage
// class. A concrete S3/MinIO Lister lives in s3.go; unit tests use a fake.
type StorageSource struct {
	Lister Lister
}

// StorageBytesByClass sums object sizes per storage class. S3 omits the
// StorageClass on STANDARD objects, so an empty class folds into STANDARD.
func (s StorageSource) StorageBytesByClass(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	err := s.Lister.EachObject(ctx, func(o Object) {
		class := o.StorageClass
		if class == "" {
			class = "STANDARD"
		}
		out[class] += o.Size
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
