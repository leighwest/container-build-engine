package cache

import (
	"errors"
	"fmt"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/cache"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"io"
	"os"
	"path/filepath"
)

// fscache is a modification of the Crane cache
// that allows explicit specification of the layer
// key.
//
// It also writes the layer straight away rather
// than doing it lazily.
type fscache struct {
	path string
}

func NewFilesystemCache(path string) Cache {
	return &fscache{
		path,
	}
}

func (fs *fscache) Put(key v1.Hash, layer v1.Layer, compressed bool) (v1.Layer, error) {
	var rc io.ReadCloser
	var err error
	if compressed {
		rc, err = layer.Compressed()
	} else {
		rc, err = layer.Uncompressed()
	}
	if err != nil {
		return nil, fmt.Errorf("reading layer: %w", err)
	}
	// closing the reader releases resources held by the
	// underlying layer. For remote layers this includes the
	// go-containerregistry pull limiter token, so failing to
	// close it leaks a token and eventually deadlocks the build.
	defer rc.Close()

	mediaType, err := layer.MediaType()
	if err != nil {
		return nil, fmt.Errorf("getting media type: %w", err)
	}

	// write to a temporary file and rename it into place
	// so that a failed copy never leaves a partial layer
	// in the cache.
	if err := os.MkdirAll(fs.path, 0700); err != nil {
		return nil, fmt.Errorf("preparing fs: %w", err)
	}
	path := cachepath(fs.path, key)
	w, err := os.CreateTemp(fs.path, filepath.Base(path)+".tmp-*")
	if err != nil {
		return nil, fmt.Errorf("preparing fs: %w", err)
	}
	tmp := w.Name()
	if _, err := io.Copy(w, rc); err != nil {
		_ = w.Close()
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("writing layer: %w", err)
	}
	if err := w.Close(); err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("writing layer: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("writing layer: %w", err)
	}

	// return a layer backed by the file we just wrote rather
	// than the original layer, so that later reads don't go
	// back to the original (possibly remote) source.
	l, err := tarball.LayerFromFile(path, tarball.WithMediaType(mediaType))
	if err != nil {
		return nil, fmt.Errorf("reading cached layer: %w", err)
	}
	return l, nil
}

func (fs *fscache) Get(hash v1.Hash) (v1.Layer, error) {
	path := cachepath(fs.path, hash)
	// try to read the layer from a file
	l, err := tarball.LayerFromFile(path)
	if os.IsNotExist(err) {
		return nil, cache.ErrNotFound
	}
	// if it's somehow corrupt, delete it
	// so we can fix it next time around
	if errors.Is(err, io.ErrUnexpectedEOF) {
		if err := fs.Delete(hash); err != nil {
			return nil, err
		}
		return nil, cache.ErrNotFound
	}
	return l, nil
}

func (fs *fscache) Delete(hash v1.Hash) error {
	if err := os.Remove(cachepath(fs.path, hash)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cache.ErrNotFound
		}
		return err
	}
	return nil
}

const layerDir = "cbe"

// Dir attempts to find a directory that we can store the
// cached layers in.
// It will try the XDG_CACHE_HOME first, followed by ~/.cache
// and finally TMPDIR or /tmp
func Dir() string {
	cacheHome := os.Getenv("XDG_CACHE_HOME")
	if cacheHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.TempDir()
		}
		cacheHome = filepath.Join(home, ".cache")
	}
	cacheHome = filepath.Join(cacheHome, layerDir)
	_ = os.MkdirAll(cacheHome, 0750)
	return cacheHome
}
