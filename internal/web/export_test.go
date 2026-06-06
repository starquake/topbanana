package web

import (
	"io/fs"

	"github.com/starquake/topbanana/internal/config"
)

// ExportShellAssetPaths re-exports shellAssetPaths so an external test can pin
// it against the PRECACHE_URLS list in sw.js: the cache version is hashed over
// exactly these paths, so a precache entry that is not also a shell asset would
// not invalidate the cache on its next change.
func ExportShellAssetPaths() []string {
	return shellAssetPaths()
}

// ExportEmbeddedStaticFS re-exports the embedded static tree so a test can read
// sw.js straight from the bytes the binary ships, without standing up a server.
func ExportEmbeddedStaticFS() fs.FS {
	return resolveStaticFS(&config.Config{})
}
