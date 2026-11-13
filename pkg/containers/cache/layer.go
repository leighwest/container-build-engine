package cache

import (
	"fmt"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"path/filepath"
	"runtime"
)

func cachepath(path string, h v1.Hash) string {
	file := h.String()
	if runtime.GOOS == "windows" {
		file = fmt.Sprintf("%s-%s", h.Algorithm, h.Hex)
	}
	return filepath.Join(path, file)
}
