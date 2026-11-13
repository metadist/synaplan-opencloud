// Package synced keeps the list of searchable files a person asked to keep
// up to date: which OpenCloud file, in which knowledge folder, and the
// version Synaplan last took. Synaplan holds the copies but offers no way
// to list a person's kept files by their OpenCloud id, and OpenCloud does
// not tell an extension when a file changes, so the background pass reads
// this list to know what to check. It lives in OpenCloud's NATS key-value
// store next to the offline tokens, one key per person and file, so a
// restart or another replica of this service still has it.
package synced

import (
	"context"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/nats-io/nats.go/jetstream"
)

// File is one searchable file kept up to date for a person. Revision is
// the generation it was read at; Update and Delete name it, so a replica
// never moves a version back over a newer one another replica wrote.
type File struct {
	Subject    string `json:"-"`
	ResourceID string `json:"-"`
	Etag       string `json:"etag"`
	GroupKey   string `json:"groupKey"`
	Revision   uint64 `json:"-"`
}

// Store keeps the files of every person.
type Store interface {
	// Put records file, replacing what was kept for it.
	Put(ctx context.Context, file File) error
	// Update replaces file when its kept revision is still file.Revision.
	// ok is false when another write came first or it is gone.
	Update(ctx context.Context, file File) (ok bool, err error)
	// Delete forgets a file when its revision is still match, or whatever
	// is kept for it when match is 0. Forgetting a file that is not kept
	// is not an error.
	Delete(ctx context.Context, subject, resourceID string, match uint64) error
	// Files lists the files kept for subject.
	Files(ctx context.Context, subject string) ([]File, error)
}

// NATSStore is the JetStream key-value bucket that keeps the files.
type NATSStore struct {
	js     jetstream.JetStream
	bucket jetstream.KeyValueConfig

	mu sync.Mutex
	kv jetstream.KeyValue
}

// NewNATS keeps the files in bucket, on the JetStream connection the
// offline tokens already use. The bucket is opened, or created, on first
// use, so this service starts while NATS is still starting.
func NewNATS(js jetstream.JetStream, bucket string, replicas int) (*NATSStore, error) {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return nil, errors.New("synced: nats-js-kv needs a bucket")
	}
	if replicas < 0 {
		return nil, errors.New("synced: nats-js-kv replicas must not be negative")
	}
	return &NATSStore{js: js, bucket: jetstream.KeyValueConfig{
		Bucket:      bucket,
		Description: "Searchable files Synaplan keeps up to date",
		History:     1,
		Storage:     jetstream.FileStorage,
		Replicas:    replicas,
	}}, nil
}

func (s *NATSStore) open(ctx context.Context) (jetstream.KeyValue, error) {
	s.mu.Lock()
	kv := s.kv
	s.mu.Unlock()
	if kv != nil {
		return kv, nil
	}
	kv, err := s.js.CreateOrUpdateKeyValue(ctx, s.bucket)
	if err != nil {
		return nil, fmt.Errorf("synced: opening the %s bucket: %w", s.bucket.Bucket, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.kv == nil {
		s.kv = kv
	}
	return s.kv, nil
}

// Put records file.
func (s *NATSStore) Put(ctx context.Context, file File) error {
	kv, err := s.open(ctx)
	if err != nil {
		return err
	}
	value, err := json.Marshal(file)
	if err != nil {
		return fmt.Errorf("synced: encoding: %w", err)
	}
	if _, err := kv.Put(ctx, Key(file.Subject, file.ResourceID), value); err != nil {
		return fmt.Errorf("synced: writing: %w", err)
	}
	return nil
}

// Update replaces file when its revision is still current.
func (s *NATSStore) Update(ctx context.Context, file File) (bool, error) {
	kv, err := s.open(ctx)
	if err != nil {
		return false, err
	}
	value, err := json.Marshal(file)
	if err != nil {
		return false, fmt.Errorf("synced: encoding: %w", err)
	}
	_, err = kv.Update(ctx, Key(file.Subject, file.ResourceID), value, file.Revision)
	if errors.Is(err, jetstream.ErrKeyRevisionMismatch) || errors.Is(err, jetstream.ErrKeyNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("synced: updating: %w", err)
	}
	return true, nil
}

// Delete purges the file's key, so a forgotten file leaves nothing behind.
func (s *NATSStore) Delete(ctx context.Context, subject, resourceID string, match uint64) error {
	kv, err := s.open(ctx)
	if err != nil {
		return err
	}
	var opts []jetstream.KVDeleteOpt
	if match != 0 {
		opts = append(opts, jetstream.LastRevision(match))
	}
	err = kv.Purge(ctx, Key(subject, resourceID), opts...)
	if err == nil || errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) ||
		errors.Is(err, jetstream.ErrKeyRevisionMismatch) {
		return nil
	}
	return fmt.Errorf("synced: forgetting: %w", err)
}

// Files lists the files kept for subject.
func (s *NATSStore) Files(ctx context.Context, subject string) ([]File, error) {
	kv, err := s.open(ctx)
	if err != nil {
		return nil, err
	}
	lister, err := kv.ListKeysFiltered(ctx, encode(subject)+".*")
	if err != nil {
		return nil, fmt.Errorf("synced: listing: %w", err)
	}
	defer func() { _ = lister.Stop() }()
	var files []File
	for key := range lister.Keys() {
		entry, err := kv.Get(ctx, key)
		if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("synced: reading: %w", err)
		}
		file, ok := decode(key, entry.Value())
		if !ok {
			continue
		}
		file.Revision = entry.Revision()
		files = append(files, file)
	}
	return files, nil
}

// MemoryStore keeps the files in this process: a restart forgets them and
// another replica never sees them. It goes with the memory token store.
type MemoryStore struct {
	mu       sync.Mutex
	revision uint64
	files    map[string]File
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{files: map[string]File{}}
}

// Put records file.
func (s *MemoryStore) Put(_ context.Context, file File) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision++
	file.Revision = s.revision
	s.files[Key(file.Subject, file.ResourceID)] = file
	return nil
}

// Update replaces file when its revision is still current.
func (s *MemoryStore) Update(_ context.Context, file File) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := Key(file.Subject, file.ResourceID)
	kept, ok := s.files[key]
	if !ok || kept.Revision != file.Revision {
		return false, nil
	}
	s.revision++
	file.Revision = s.revision
	s.files[key] = file
	return true, nil
}

// Delete forgets a file when its revision is still match, or always when
// match is 0.
func (s *MemoryStore) Delete(_ context.Context, subject, resourceID string, match uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := Key(subject, resourceID)
	if kept, ok := s.files[key]; ok && (match == 0 || kept.Revision == match) {
		delete(s.files, key)
	}
	return nil
}

// Files lists the files kept for subject, by resource id.
func (s *MemoryStore) Files(_ context.Context, subject string) ([]File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	files := make([]File, 0, len(s.files))
	for _, file := range s.files {
		if file.Subject == subject {
			files = append(files, file)
		}
	}
	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.ResourceID, b.ResourceID) })
	return files, nil
}

// Key is the bucket key of one person's file: both parts encoded, because a
// subject or an OpenCloud resource id may hold characters NATS rejects in
// a key, joined by a dot so one person's files are listed by a filter.
func Key(subject, resourceID string) string {
	return encode(subject) + "." + encode(resourceID)
}

func encode(s string) string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(s))
}

// decode reads a value back with the subject and resource id of its key.
func decode(key string, value []byte) (File, bool) {
	parts := strings.Split(key, ".")
	if len(parts) != 2 {
		return File{}, false
	}
	subject, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(parts[0])
	if err != nil {
		return File{}, false
	}
	resourceID, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(parts[1])
	if err != nil {
		return File{}, false
	}
	var file File
	if err := json.Unmarshal(value, &file); err != nil {
		return File{}, false
	}
	file.Subject = string(subject)
	file.ResourceID = string(resourceID)
	return file, true
}
