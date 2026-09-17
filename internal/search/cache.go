package search

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/index/scorch"
	"github.com/blevesearch/bleve/v2/mapping"

	"github.com/toppynl/vaulty/internal/name"
	"github.com/toppynl/vaulty/internal/vault"
)

// Cache layout below Dir(root) (DESIGN.md §19.3).
const (
	indexDirName    = "index"
	manifestName    = "manifest.json"
	lockName        = "lock"
	lockTimeout     = 2 * time.Second
	lockPoll        = 20 * time.Millisecond
	indexBatchSize  = 200
	updateKindFull  = "full"
	updateKindIncr  = "incremental"
	boltOpenTimeout = "2s"
)

var errLockTimeout = fmt.Errorf("index locked by another process for more than %s", lockTimeout)

// keepIndexError marks a failure that makes the cache unusable for this call
// without saying anything about the index itself (e.g. the manifest can't be
// removed in a read-only cache dir): fall back to memory, but don't delete
// a possibly valid index.
type keepIndexError struct{ err error }

func (e keepIndexError) Error() string { return e.err.Error() }
func (e keepIndexError) Unwrap() error { return e.err }

// Manifest records what the cached index holds, so each call can bring it
// up to date incrementally (DESIGN.md §19.3).
type Manifest struct {
	Format       int              `json:"format"`
	MappingHash  string           `json:"mapping_hash"`
	ConfigHash   string           `json:"config_hash"`
	Pages        map[string]Entry `json:"pages"`
	LastUpdate   time.Time        `json:"last_update"`
	LastChecked  int              `json:"last_check_updated"` // pages indexed or deleted by the most recent update (page count after a full rebuild)
	LastKind     string           `json:"last_update_kind"`   // full | incremental
	FullRebuilds int              `json:"full_rebuilds"`
}

// Entry is one page as last indexed.
type Entry struct {
	Size    int64  `json:"size"`
	MtimeNS int64  `json:"mtime_ns"`
	SHA256  string `json:"sha256"`
	Summary string `json:"summary"`
}

// Dir is the cache directory for the vault at root:
// $VAULTY_CACHE_DIR/<sha256(root)[:16]>, else
// os.UserCacheDir()/vaulty/<sha256(root)[:16]>.
func Dir(root string) (string, error) {
	base := os.Getenv(name.EnvCacheDir)
	if base == "" {
		ucd, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(ucd, name.Binary)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(base, hex.EncodeToString(sum[:])[:16]), nil
}

// configHash fingerprints the config that decides what gets indexed and how
// a page is split; a change forces a full rebuild.
func configHash(v *vault.Vault) string {
	c := v.Config
	b, _ := json.Marshal(struct {
		Root      string   `json:"root"`
		Dirs      []string `json:"dirs"`
		Exclude   []string `json:"exclude"`
		Index     string   `json:"index"`
		Heading   string   `json:"heading"`
		Divider   string   `json:"divider"`
		Analyzers []string `json:"analyzers"`
	}{v.Root, c.Dirs, c.Exclude, c.Find.Index, c.Timeline.Heading, c.Timeline.Divider, c.Search.Analyzers})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func readManifest(dir string) *Manifest {
	b, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return nil
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil || m.Pages == nil {
		return nil
	}
	return &m
}

// writeManifest writes atomically: temp file in the same dir, then rename.
func writeManifest(dir string, m *Manifest) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, manifestName+".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, manifestName)); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// cachedIndex is an opened, up-to-date cached index; close releases the
// index and the lock.
type cachedIndex struct {
	idx     bleve.Index
	dir     string
	release func()
}

func (c *cachedIndex) close() {
	if c.idx != nil {
		_ = c.idx.Close()
	}
	c.release()
}

// discard closes the index and deletes it, for a cache that turned out to be
// unusable mid-query; the next call rebuilds it.
func (c *cachedIndex) discard() {
	if c.idx != nil {
		_ = c.idx.Close()
		c.idx = nil
	}
	_ = os.Remove(filepath.Join(c.dir, manifestName))
	_ = os.RemoveAll(filepath.Join(c.dir, indexDirName))
	c.release()
}

// openCached locks the vault's cache dir, brings the index up to date with
// corpus (full rebuild or incremental), and returns it. Any error means the
// cache is unusable for this call; the caller falls back to memory.
func openCached(v *vault.Vault, corp *corpus, im *mapping.IndexMappingImpl, forceRebuild bool) (ci *cachedIndex, err error) {
	dir, err := Dir(v.Root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	release, err := lockFile(filepath.Join(dir, lockName), lockTimeout)
	if err != nil {
		return nil, err
	}
	ci = &cachedIndex{dir: dir, release: release}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("index panic: %v", r)
		}
		if err != nil {
			var keep keepIndexError
			if errors.As(err, &keep) {
				ci.close()
			} else {
				ci.discard()
			}
			ci = nil
		}
	}()

	mHash, err := mappingHash(im)
	if err != nil {
		return ci, err
	}
	cHash := configHash(v)
	old := readManifest(dir)
	idxPath := filepath.Join(dir, indexDirName)

	full := forceRebuild || old == nil || old.Format != FormatVersion || old.MappingHash != mHash || old.ConfigHash != cHash
	if !full {
		idx, oerr := bleve.OpenUsing(idxPath, map[string]any{"bolt_timeout": boltOpenTimeout})
		if oerr != nil {
			full = true // corrupt or missing: rebuild, never fail
		} else {
			ci.idx = idx
		}
	}

	if full {
		if ci.idx != nil {
			_ = ci.idx.Close()
			ci.idx = nil
		}
		if err := removeIfExists(filepath.Join(dir, manifestName)); err != nil {
			return ci, keepIndexError{err}
		}
		if err := os.RemoveAll(idxPath); err != nil {
			return ci, err
		}
		idx, err := bleve.NewUsing(idxPath, im, scorch.Name, scorch.Name, map[string]any{"bolt_timeout": boltOpenTimeout})
		if err != nil {
			return ci, err
		}
		ci.idx = idx
		pages := map[string]Entry{}
		if err := indexAll(idx, corp, pages); err != nil {
			return ci, err
		}
		m := &Manifest{
			Format: FormatVersion, MappingHash: mHash, ConfigHash: cHash, Pages: pages,
			LastUpdate: time.Now().UTC(), LastChecked: len(pages), LastKind: updateKindFull, FullRebuilds: 1,
		}
		if old != nil {
			m.FullRebuilds = old.FullRebuilds + 1
		}
		// The index is complete; a manifest that can't be written only
		// means the next call rebuilds again. Use the index regardless.
		_ = writeManifest(dir, m)
		return ci, nil
	}

	return ci, updateIncremental(ci, corp, old)
}

func removeIfExists(p string) error {
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// indexAll indexes every corpus page in batches, filling pages.
func indexAll(idx bleve.Index, corp *corpus, pages map[string]Entry) error {
	b := idx.NewBatch()
	for _, f := range corp.files {
		pg, e, err := corp.load(f)
		if err != nil {
			continue // unreadable: not indexed
		}
		if err := b.Index(f.rel, pg.bleveDoc()); err != nil {
			return err
		}
		pages[f.rel] = e
		if b.Size() >= indexBatchSize {
			if err := idx.Batch(b); err != nil {
				return err
			}
			b = idx.NewBatch()
		}
	}
	if b.Size() > 0 {
		return idx.Batch(b)
	}
	return nil
}

// updateIncremental applies the difference between the manifest and the
// corpus: size+mtime equal (and the same index summary) = unchanged; else
// the content hash decides between "only metadata moved" and "reindex".
func updateIncremental(ci *cachedIndex, corp *corpus, old *Manifest) error {
	m := *old
	m.Pages = make(map[string]Entry, len(old.Pages))
	dirty := false
	changed := 0
	b := ci.idx.NewBatch()
	present := map[string]bool{}

	for _, f := range corp.files {
		present[f.rel] = true
		summary := corp.summaries[f.slug()]
		prev, had := old.Pages[f.rel]
		if had && prev.Size == f.size && prev.MtimeNS == f.mtimeNS && prev.Summary == summary {
			m.Pages[f.rel] = prev
			continue
		}
		pg, e, err := corp.load(f)
		if err != nil {
			if had {
				b.Delete(f.rel)
				changed++
			}
			dirty = true
			continue
		}
		dirty = true
		if had && prev.SHA256 == e.SHA256 && prev.Summary == e.Summary {
			m.Pages[f.rel] = e // touched, not changed
			continue
		}
		if err := b.Index(f.rel, pg.bleveDoc()); err != nil {
			return err
		}
		m.Pages[f.rel] = e
		changed++
	}
	for rel := range old.Pages {
		if !present[rel] {
			b.Delete(rel)
			changed++
			dirty = true
		}
	}

	if b.Size() > 0 {
		if err := ci.idx.Batch(b); err != nil {
			return err
		}
	}
	m.LastChecked = changed
	if changed > 0 {
		m.LastUpdate = time.Now().UTC()
		m.LastKind = updateKindIncr
	} else {
		m.LastKind = old.LastKind
		m.LastChecked = old.LastChecked
	}
	if dirty {
		// Best effort: the index is already updated, and a stale manifest
		// only makes the next call re-apply the same idempotent updates.
		_ = writeManifest(ci.dir, &m)
	}
	return nil
}

// Stats describes the cached index for `search --stats`.
type Stats struct {
	CachePath    string    `json:"cache_path"`
	Exists       bool      `json:"exists"`
	Pages        int       `json:"pages"`
	IndexBytes   int64     `json:"index_bytes"`
	LastUpdate   time.Time `json:"last_update"`
	LastChecked  int       `json:"last_check_updated"`
	LastKind     string    `json:"last_update_kind"`
	FullRebuilds int       `json:"full_rebuilds"`
}

// ReadStats reports on the cached index without touching it (no lock, no
// freshness check): the numbers are those of the most recent search call.
func ReadStats(v *vault.Vault) (*Stats, error) {
	dir, err := Dir(v.Root)
	if err != nil {
		return nil, err
	}
	st := &Stats{CachePath: dir}
	m := readManifest(dir)
	if m == nil {
		return st, nil
	}
	st.Exists = true
	st.Pages = len(m.Pages)
	st.LastUpdate = m.LastUpdate
	st.LastChecked = m.LastChecked
	st.LastKind = m.LastKind
	st.FullRebuilds = m.FullRebuilds
	_ = filepath.WalkDir(filepath.Join(dir, indexDirName), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			st.IndexBytes += info.Size()
		}
		return nil
	})
	return st, nil
}
