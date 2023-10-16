/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package remotes

import (
	"context"
	_ "crypto/sha256"
	"encoding/json"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/images"
)

func TestContextCustomKeyPrefix(t *testing.T) {
	ctx := context.Background()
	cmt := "testing/custom.media.type"
	ctx = WithMediaTypeKeyPrefix(ctx, images.MediaTypeDockerSchema2Layer, "bananas")
	ctx = WithMediaTypeKeyPrefix(ctx, cmt, "apples")

	// makes sure that even though we've supplied some custom handling, the built-in still works
	t.Run("normal supported case", func(t *testing.T) {
		desc := imagespec.Descriptor{MediaType: imagespec.MediaTypeImageLayer}
		expected := "layer-"

		actual := MakeRefKey(ctx, desc)
		if actual != expected {
			t.Fatalf("unexpected ref key, expected %s, got: %s", expected, actual)
		}
	})

	t.Run("unknown media type", func(t *testing.T) {
		desc := imagespec.Descriptor{MediaType: "we.dont.know.what.this.is"}
		expected := "unknown-"

		actual := MakeRefKey(ctx, desc)
		if actual != expected {
			t.Fatalf("unexpected ref key, expected %s, got: %s", expected, actual)
		}
	})

	t.Run("overwrite supported media type", func(t *testing.T) {
		desc := imagespec.Descriptor{MediaType: images.MediaTypeDockerSchema2Layer}
		expected := "bananas-"

		actual := MakeRefKey(ctx, desc)
		if actual != expected {
			t.Fatalf("unexpected ref key, expected %s, got: %s", expected, actual)
		}
	})

	t.Run("custom media type", func(t *testing.T) {
		desc := imagespec.Descriptor{MediaType: cmt}
		expected := "apples-"

		actual := MakeRefKey(ctx, desc)
		if actual != expected {
			t.Fatalf("unexpected ref key, expected %s, got: %s", expected, actual)
		}
	})
}

//nolint:staticcheck // Non-distributable layers are deprecated
func TestSkipNonDistributableBlobs(t *testing.T) {
	ctx := context.Background()

	out, err := SkipNonDistributableBlobs(images.HandlerFunc(func(ctx context.Context, desc imagespec.Descriptor) ([]imagespec.Descriptor, error) {
		return []imagespec.Descriptor{
			{MediaType: images.MediaTypeDockerSchema2Layer, Digest: "test:1"},
			{MediaType: images.MediaTypeDockerSchema2LayerForeign, Digest: "test:2"},
			{MediaType: images.MediaTypeDockerSchema2LayerForeignGzip, Digest: "test:3"},
			{MediaType: imagespec.MediaTypeImageLayerNonDistributable, Digest: "test:4"},
			{MediaType: imagespec.MediaTypeImageLayerNonDistributableGzip, Digest: "test:5"},
			{MediaType: imagespec.MediaTypeImageLayerNonDistributableZstd, Digest: "test:6"},
		}, nil
	}))(ctx, imagespec.Descriptor{MediaType: images.MediaTypeDockerSchema2Manifest})
	if err != nil {
		t.Fatal(err)
	}

	if len(out) != 1 {
		t.Fatalf("unexpected number of descriptors returned: %d", len(out))
	}
	if out[0].Digest != "test:1" {
		t.Fatalf("unexpected digest returned: %s", out[0].Digest)
	}

	dir := t.TempDir()
	cs, err := local.NewLabeledStore(dir, newMemoryLabelStore())
	if err != nil {
		t.Fatal(err)
	}

	write := func(i interface{}, ref string) digest.Digest {
		t.Helper()

		data, err := json.Marshal(i)
		if err != nil {
			t.Fatal(err)
		}

		w, err := cs.Writer(ctx, content.WithRef(ref))
		if err != nil {
			t.Fatal(err)
		}
		defer w.Close()

		dgst := digest.SHA256.FromBytes(data)

		n, err := w.Write(data)
		if err != nil {
			t.Fatal(err)
		}

		if err := w.Commit(ctx, int64(n), dgst); err != nil {
			t.Fatal(err)
		}

		return dgst
	}

	configDigest := write(imagespec.ImageConfig{}, "config")

	manifest := imagespec.Manifest{
		Config:    imagespec.Descriptor{Digest: configDigest, MediaType: imagespec.MediaTypeImageConfig},
		MediaType: imagespec.MediaTypeImageManifest,
		Layers: []imagespec.Descriptor{
			{MediaType: images.MediaTypeDockerSchema2Layer, Digest: "test:1"},
			{MediaType: images.MediaTypeDockerSchema2LayerForeign, Digest: "test:2"},
			{MediaType: images.MediaTypeDockerSchema2LayerForeignGzip, Digest: "test:3"},
			{MediaType: imagespec.MediaTypeImageLayerNonDistributable, Digest: "test:4"},
			{MediaType: imagespec.MediaTypeImageLayerNonDistributableGzip, Digest: "test:5"},
			{MediaType: imagespec.MediaTypeImageLayerNonDistributableZstd, Digest: "test:6"},
		},
	}

	manifestDigest := write(manifest, "manifest")

	out, err = SkipNonDistributableBlobs(images.ChildrenHandler(cs))(ctx, imagespec.Descriptor{MediaType: manifest.MediaType, Digest: manifestDigest})
	if err != nil {
		t.Fatal(err)
	}

	if len(out) != 2 {
		t.Fatalf("unexpected number of descriptors returned: %v", out)
	}

	if out[0].Digest != configDigest {
		t.Fatalf("unexpected digest returned: %v", out[0])
	}
	if out[1].Digest != manifest.Layers[0].Digest {
		t.Fatalf("unexpected digest returned: %v", out[1])
	}
}

type memoryLabelStore struct {
	l      sync.Mutex
	labels map[digest.Digest]map[string]string
}

func newMemoryLabelStore() local.LabelStore {
	return &memoryLabelStore{
		labels: map[digest.Digest]map[string]string{},
	}
}

func (mls *memoryLabelStore) Get(d digest.Digest) (map[string]string, error) {
	mls.l.Lock()
	labels := mls.labels[d]
	mls.l.Unlock()

	return labels, nil
}

func (mls *memoryLabelStore) Set(d digest.Digest, labels map[string]string) error {
	mls.l.Lock()
	mls.labels[d] = labels
	mls.l.Unlock()

	return nil
}

func (mls *memoryLabelStore) Update(d digest.Digest, update map[string]string) (map[string]string, error) {
	mls.l.Lock()
	labels, ok := mls.labels[d]
	if !ok {
		labels = map[string]string{}
	}
	for k, v := range update {
		if v == "" {
			delete(labels, k)
		} else {
			labels[k] = v
		}
	}
	mls.labels[d] = labels
	mls.l.Unlock()

	return labels, nil
}
