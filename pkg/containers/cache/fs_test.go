package cache

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// TestFSCache_PutReleasesRemoteReader checks that Put closes the
// reader it opens on the layer. Remote layers hold a pull limiter
// token until their reader is closed, so with jobs=1 a leaked
// reader blocks every subsequent fetch.
func TestFSCache_PutReleasesRemoteReader(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()

	ref, err := name.ParseReference(strings.TrimPrefix(srv.URL, "http://") + "/test/image:latest")
	if err != nil {
		t.Fatal(err)
	}

	src, err := random.Image(1024, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(ref, src); err != nil {
		t.Fatal(err)
	}

	// if a token leaks, the next fetch blocks until this
	// context expires rather than hanging the test forever
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	img, err := remote.Image(ref, remote.WithContext(ctx), remote.WithJobs(1))
	if err != nil {
		t.Fatal(err)
	}
	layers, err := img.Layers()
	if err != nil {
		t.Fatal(err)
	}

	c := NewFilesystemCache(t.TempDir())

	for i, layer := range layers {
		diffID, err := layer.DiffID()
		if err != nil {
			t.Fatal(err)
		}
		// mirror how mutate.go wraps layers before caching them
		wrapped, err := tarball.LayerFromOpener(layer.Compressed, tarball.WithMediaType(types.OCILayer))
		if err != nil {
			t.Fatal(err)
		}
		cached, err := c.Put(diffID, wrapped, true)
		if err != nil {
			t.Fatalf("layer %d: put: %v", i, err)
		}

		// the returned layer must be readable and keep its media type
		mt, err := cached.MediaType()
		if err != nil {
			t.Fatal(err)
		}
		if mt != types.OCILayer {
			t.Errorf("layer %d: expected media type %s, got %s", i, types.OCILayer, mt)
		}
		rc, err := cached.Uncompressed()
		if err != nil {
			t.Fatalf("layer %d: reading cached layer: %v", i, err)
		}
		if _, err := io.Copy(io.Discard, rc); err != nil {
			t.Fatalf("layer %d: reading cached layer: %v", i, err)
		}
		_ = rc.Close()

		gotDiffID, err := cached.DiffID()
		if err != nil {
			t.Fatal(err)
		}
		if gotDiffID != diffID {
			t.Errorf("layer %d: expected diff id %s, got %s", i, diffID, gotDiffID)
		}
	}
}
