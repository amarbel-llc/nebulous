// Package madder wraps code.linenisgreat.com/madder/go as an in-process
// content-addressed blob store. Nebulous's blobs live in a dedicated
// named store ("nebulous") inside madder's shared user-XDG tree
// (~/.local/share/madder/blob_stores/nebulous) — the same store the
// `madder` CLI resolves for `madder cat nebulous <id>` etc., opened
// in-process instead of via subprocess.
//
// Prior to this package spawned a `madder` subprocess per blob
// read/write/has call; against a large local cache (tens of thousands
// of blobs) that meant tens of thousands of subprocess launches per
// story-store build (nebulous#37). Opening the store in-process — the
// same FDR-0007 hybrid pattern circus's own nix-cache module uses —
// removes that per-call process-spawn cost entirely.
package madder

import (
	"context"
	"fmt"
	"io"
	"os"

	"code.linenisgreat.com/madder/go/pkgs/blob_io"
	"code.linenisgreat.com/madder/go/pkgs/blob_store_configs"
	"code.linenisgreat.com/madder/go/pkgs/blob_stores"
	"code.linenisgreat.com/madder/go/pkgs/directory_layout"
	"code.linenisgreat.com/madder/go/pkgs/domain_interfaces"
	"code.linenisgreat.com/madder/go/pkgs/env_dir"
	"code.linenisgreat.com/madder/go/pkgs/madder_env"
	_ "code.linenisgreat.com/madder/go/pkgs/markl_registrations" // registers the config-digest markl purpose; EncodeWithDigest panics without it
	"code.linenisgreat.com/madder/go/pkgs/scoped_id"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"

	// The markl interface vocabulary (including the concrete Id type) was
	// moved upstream to piggy (piggy#183 ownership inversion); madder's
	// own domain_interfaces.MarklId is now a type alias into this package.
	"code.linenisgreat.com/piggy/go/pkgs/markl"
)

// storeName is the fixed named store nebulous's blobs live in within
// madder's shared XDG tree, matching every prior CLI invocation
// (`madder init nebulous`, `madder cat nebulous <id>`, ...).
const storeName = "nebulous"

// Store wraps an in-process madder blob store. Safe for concurrent use
// (the underlying domain_interfaces.BlobStore is concurrency-safe).
type Store struct {
	ctx   context.Context
	env   env_dir.Env
	path  directory_layout.BlobStorePath
	blobs domain_interfaces.BlobStore
}

// NewStore resolves nebulous's named store within madder's shared
// user-XDG tree. The returned Store is not yet usable for Read/Write/Has
// until Init has been called at least once (by this process or an
// earlier `madder init nebulous`).
//
// env_dir.MakeDefault is dewey's own "bare" construction pattern (used
// this same way by madder/go's own test helpers) — but on ANY internal
// setup failure (e.g. MkdirAll on its per-pid temp dir failing, a stale
// dir left by an earlier abnormal exit, a permission mismatch) it calls
// the dewey Context's Cancel(err), which — by design — panics via a
// deferred ContextContinueOrPanic UNLESS the call runs inside a
// ctx.Run(...) wrapper that catches it (dewey's Run/runRetry does this
// internally; a bare, unwrapped call like this one does not). Confirmed
// in production (nebulous#41): a bare call here crashed the whole
// `nebulous capture` process with an opaque `panic:
// ContextStateSucceeded` instead of surfacing whatever the real
// underlying error was. The recover below converts that panic back into
// a normal Go error, retrieving the real cause via the dewey context's
// own Cause() (set by Cancel(err) before it panics) rather than losing
// it to an unhandled panic.
func NewStore(ctx context.Context) (store *Store, err error) {
	dctx := errors.MakeContextDefault()
	defer func() {
		if r := recover(); r != nil {
			if cause := dctx.Cause(); cause != nil {
				err = fmt.Errorf("madder store: env setup: %w", cause)
			} else {
				err = fmt.Errorf("madder store: env setup panicked: %v", r)
			}
		}
	}()

	cfg := env_dir.Config{EnvVarNames: madder_env.DefaultEnvVarNames}
	env := env_dir.MakeDefault(dctx, cfg, "madder")

	var id scoped_id.Id
	_ = id.Set(storeName)

	layout, layoutErr := directory_layout.MakeBlobStore(env.GetXDGForBlobStoreId(id))
	if layoutErr != nil {
		// Deferred: NewStore historically never fails on path resolution
		// alone. Init/Read/Write/Has surface the real error.
		return &Store{ctx: ctx, env: env}, nil
	}

	return &Store{
		ctx:  ctx,
		env:  env,
		path: directory_layout.GetBlobStorePath(layout, id.GetName()),
	}, nil
}

// Init ensures the nebulous blob store is present, creating it with
// madder's default hash/compression config if absent. Safe to call more
// than once — an existing store's config is left untouched.
func (s *Store) Init() error {
	if err := os.MkdirAll(s.path.GetBase(), 0o755); err != nil {
		return errors.Wrapf(err, "madder store: create %s", s.path.GetBase())
	}

	if err := writeDefaultConfig(s.path.GetConfig()); err != nil {
		return errors.Wrapf(err, "madder store: init %s", storeName)
	}

	typedConfig, err := blob_store_configs.DecodeAndVerifyFromFile(s.path.GetConfig())
	if err != nil {
		return errors.Wrapf(err, "madder store: read config for %s", storeName)
	}

	cfgNamed := blob_store_configs.ConfigNamed{Path: s.path, Config: typedConfig}
	blobs, err := blob_stores.MakeBlobStore(s.env, cfgNamed, nil)
	if err != nil {
		return errors.Wrapf(err, "madder store: open %s", storeName)
	}

	s.blobs = blobs
	return nil
}

// writeDefaultConfig idempotently creates the store's blob_store-config
// file with madder's default hash type, bucket count, and zstd
// compression (no encryption) — the in-process equivalent of
// `madder init -encryption none <storeId>`. A no-op when the config
// already exists.
func writeDefaultConfig(configPath string) error {
	f, err := os.OpenFile(configPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	toml := &blob_store_configs.TomlV3{
		HashTypeId:      blob_store_configs.HashTypeDefault,
		HashBuckets:     blob_store_configs.DefaultHashBuckets,
		CompressionType: "zstd",
	}
	cfg := blob_store_configs.TypedConfig{
		Type: blob_store_configs.TypeStructForConfig(toml),
		Blob: toml,
	}
	_, err = blob_store_configs.EncodeWithDigest(&cfg, f)
	return err
}

// Read streams the blob identified by id to dst. Returns (false, nil) if
// the blob is absent; (false, err) on other I/O failures.
func (s *Store) Read(id string, dst io.Writer) (bool, error) {
	marklId, err := parseMarklId(id)
	if err != nil {
		return false, err
	}

	r, err := s.blobs.MakeBlobReader(marklId)
	if err != nil {
		if blob_io.IsErrBlobMissing(err) {
			return false, nil
		}
		return false, errors.Wrapf(err, "madder store: read %s", id)
	}
	defer r.Close()

	if _, err := io.Copy(dst, r); err != nil {
		return false, errors.Wrapf(err, "madder store: read %s", id)
	}
	return true, nil
}

// Write consumes src and returns the markl-id madder assigned to the blob.
func (s *Store) Write(src io.Reader) (string, error) {
	w, err := s.blobs.MakeBlobWriter(nil) // nil -> store's default hash
	if err != nil {
		return "", errors.Wrapf(err, "madder store: write")
	}
	if _, err := io.Copy(w, src); err != nil {
		w.Close()
		return "", errors.Wrapf(err, "madder store: write")
	}
	if err := w.Close(); err != nil { // Close commits the blob
		return "", errors.Wrapf(err, "madder store: write")
	}
	return w.GetMarklId().String(), nil
}

// Has reports whether the store holds a blob for id.
func (s *Store) Has(id string) (bool, error) {
	marklId, err := parseMarklId(id)
	if err != nil {
		return false, err
	}
	return s.blobs.HasBlob(marklId), nil
}

func parseMarklId(id string) (*markl.Id, error) {
	var marklId markl.Id
	if err := marklId.Set(id); err != nil {
		return nil, errors.Wrapf(err, "madder store: parse markl id %q", id)
	}
	return &marklId, nil
}
