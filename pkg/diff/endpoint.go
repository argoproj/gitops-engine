package diff

import (
	"bytes"
	"crypto/md5"
	"hash"

	"github.com/davecgh/go-spew/spew"
	discoveryv1 "k8s.io/api/discovery/v1"
)

type ports []discoveryv1.EndpointPort

func (p ports) Len() int { return len(p) }

func (p ports) Less(i, j int) bool {
	hasher := md5.New()
	h1 := hashObject(hasher, p[i])
	h2 := hashObject(hasher, p[j])
	return bytes.Compare(h1, h2) < 0
}

func (p ports) Swap(i, j int) { p[i], p[j] = p[j], p[i] }

func hashObject(hasher hash.Hash, obj any) []byte {
	DeepHashObject(hasher, obj)
	return hasher.Sum(nil)
}

// DeepHashObject writes specified object to hash using the spew library
// which follows pointers and prints actual values of the nested objects
// ensuring the hash does not change when a pointer changes.
func DeepHashObject(hasher hash.Hash, objectToWrite any) {
	hasher.Reset()
	printer := spew.ConfigState{
		Indent:         " ",
		SortKeys:       true,
		DisableMethods: true,
		SpewKeys:       true,
	}
	printer.Fprintf(hasher, "%#v", objectToWrite)
}
