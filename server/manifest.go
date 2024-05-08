package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ollama/ollama/types/model"
)

type Manifest struct {
	ManifestV2

	filepath string
	fi       os.FileInfo
	digest   string
}

func (m *Manifest) Size() (size int64) {
	for _, layer := range append(m.Layers, m.Config) {
		size += layer.Size
	}

	return
}

func (m *Manifest) Remove() error {
	if err := os.Remove(m.filepath); err != nil {
		return err
	}

	manifests, err := GetManifestPath()
	if err != nil {
		return err
	}

	return pruneEmptyDirectory(manifests)
}

func pruneEmptyDirectory(p string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}

	if fi.Mode()&os.ModeSymlink == 0 {
		entries, err := os.ReadDir(p)
		if err != nil {
			return err
		}

		for _, entry := range entries {
			if entry.IsDir() {
				if err := pruneEmptyDirectory(filepath.Join(p, entry.Name())); err != nil {
					return err
				}
			}
		}

		entries, err = os.ReadDir(p)
		if err != nil {
			return err
		}

		if len(entries) == 0 {
			if err := os.Remove(p); err != nil {
				return err
			}
		}
	}

	return nil
}

func (m *Manifest) RemoveLayers() error {
	for _, layer := range append(m.Layers, m.Config) {
		if err := layer.Remove(); err != nil {
			return err
		}
	}

	return nil
}

func ParseNamedManifest(n model.Name) (*Manifest, error) {
	if !n.IsFullyQualified() {
		return nil, model.Unqualified(n)
	}

	manifests, err := GetManifestPath()
	if err != nil {
		return nil, err
	}

	p := filepath.Join(manifests, n.Filepath())

	var m ManifestV2
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}

	sha256sum := sha256.New()
	if err := json.NewDecoder(io.TeeReader(f, sha256sum)).Decode(&m); err != nil {
		return nil, err
	}

	return &Manifest{
		ManifestV2: m,
		filepath:   p,
		fi:         fi,
		digest:     fmt.Sprintf("%x", sha256sum.Sum(nil)),
	}, nil
}

func WriteManifest(name model.Name, config *Layer, layers []*Layer) error {
	manifests, err := GetManifestPath()
	if err != nil {
		return err
	}

	p := filepath.Join(manifests, name.Filepath())
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}

	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()

	m := ManifestV2{
		SchemaVersion: 2,
		MediaType:     "application/vnd.docker.distribution.manifest.v2+json",
		Config:        config,
		Layers:        layers,
	}

	return json.NewEncoder(f).Encode(m)
}

func Manifests() (map[model.Name]*Manifest, error) {
	manifests, err := GetManifestPath()
	if err != nil {
		return nil, err
	}

	// TODO(mxyng): use something less brittle
	matches, err := filepath.Glob(filepath.Join(manifests, "*", "*", "*", "*"))
	if err != nil {
		return nil, err
	}

	ms := make(map[model.Name]*Manifest)
	for _, match := range matches {
		fi, err := os.Stat(match)
		if err != nil {
			return nil, err
		}

		if !fi.IsDir() {
			rel, err := filepath.Rel(manifests, match)
			if err != nil {
				slog.Warn("bad filepath", "path", match, "error", err)
				continue
			}

			n := model.ParseNameFromFilepath(rel)
			if !n.IsValid() {
				slog.Warn("bad manifest name", "path", rel, "error", err)
				continue
			}

			m, err := ParseNamedManifest(n)
			if err != nil {
				slog.Warn("bad manifest", "name", n, "error", err)
				continue
			}

			ms[n] = m
		}
	}

	return ms, nil
}
