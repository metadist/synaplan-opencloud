package delegation

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/nats-io/nats.go/jetstream"
)

// Settings keeps one small document per person and name beside the offline
// tokens, in the same store, so every replica reads the same one and a
// write names the revision it read. A revision of 0 means none is kept.
//
// A setting lives here when this service is its record: Synaplan stores
// what a cloud reports about a person, a folder read scope for one, but a
// cloud cannot read it back, so a change has to start from what this
// service kept.
type Settings interface {
	// GetSetting returns the person's document name and its revision.
	GetSetting(ctx context.Context, name, subject string) (value []byte, revision uint64, err error)
	// PutSetting stores value when the kept revision is still match. match
	// 0 stores it only when nothing is kept yet. ok is false when the
	// revision moved.
	PutSetting(ctx context.Context, name, subject string, value []byte, match uint64) (revision uint64, ok bool, err error)
}

// settingName keeps a name usable as the first token of a NATS key. A dot
// in the key keeps settings out of Subjects, as it does for leases.
var settingName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

var errNoSettings = errors.New("delegation: the store keeps no settings")

func checkSettingName(name string) error {
	if !settingName.MatchString(name) || name == "lease" {
		return fmt.Errorf("delegation: %q is not a setting name", name)
	}
	return nil
}

// settingKey is the NATS key of a person's setting.
func settingKey(name, subject string) string {
	return name + "." + natsKey(subject)
}

// GetSetting reads a person's setting.
func (s *NATSStore) GetSetting(ctx context.Context, name, subject string) ([]byte, uint64, error) {
	if err := checkSettingName(name); err != nil {
		return nil, 0, err
	}
	kv, err := s.open(ctx)
	if err != nil {
		return nil, 0, err
	}
	entry, err := kv.Get(ctx, settingKey(name, subject))
	if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("delegation: reading the %s setting: %w", name, err)
	}
	return entry.Value(), entry.Revision(), nil
}

// PutSetting stores a person's setting when match is still current.
func (s *NATSStore) PutSetting(ctx context.Context, name, subject string, value []byte, match uint64) (uint64, bool, error) {
	if err := checkSettingName(name); err != nil {
		return 0, false, err
	}
	kv, err := s.open(ctx)
	if err != nil {
		return 0, false, err
	}
	key := settingKey(name, subject)
	var rev uint64
	if match == 0 {
		rev, err = kv.Create(ctx, key, value)
		if keyExists(err) {
			return 0, false, nil
		}
	} else {
		rev, err = kv.Update(ctx, key, value, match)
		if errors.Is(err, jetstream.ErrKeyRevisionMismatch) || errors.Is(err, jetstream.ErrKeyNotFound) {
			return 0, false, nil
		}
	}
	if err != nil {
		return 0, false, fmt.Errorf("delegation: writing the %s setting: %w", name, err)
	}
	return rev, true, nil
}

// GetSetting reads a person's setting.
func (s *MemoryStore) GetSetting(_ context.Context, name, subject string) ([]byte, uint64, error) {
	if err := checkSettingName(name); err != nil {
		return nil, 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.settings[name+"\x00"+subject]
	if !ok {
		return nil, 0, nil
	}
	return []byte(entry.Token), entry.Revision, nil
}

// PutSetting stores a person's setting when match is still current.
func (s *MemoryStore) PutSetting(_ context.Context, name, subject string, value []byte, match uint64) (uint64, bool, error) {
	if err := checkSettingName(name); err != nil {
		return 0, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := name + "\x00" + subject
	entry, ok := s.settings[key]
	if (match == 0 && ok) || (match != 0 && (!ok || entry.Revision != match)) {
		return 0, false, nil
	}
	s.revision++
	s.settings[key] = Entry{Token: string(value), Revision: s.revision}
	return s.revision, true, nil
}

// settingSubject is what a setting's seal is bound to: the person and the
// name, so a setting does not decrypt as a token or as another setting.
func settingSubject(name, subject string) string {
	return "setting\x00" + name + "\x00" + subject
}

// GetSetting reads and decrypts a person's setting. Folder paths name what
// a person keeps, so they are sealed like the tokens.
func (s *SealedStore) GetSetting(ctx context.Context, name, subject string) ([]byte, uint64, error) {
	inner, ok := s.inner.(Settings)
	if !ok {
		return nil, 0, errNoSettings
	}
	value, rev, err := inner.GetSetting(ctx, name, subject)
	if err != nil || rev == 0 {
		return nil, rev, err
	}
	plain, _, err := s.unseal(ctx, settingSubject(name, subject), string(value))
	if err != nil {
		return nil, rev, err
	}
	return []byte(plain), rev, nil
}

// PutSetting seals and stores a person's setting.
func (s *SealedStore) PutSetting(ctx context.Context, name, subject string, value []byte, match uint64) (uint64, bool, error) {
	inner, ok := s.inner.(Settings)
	if !ok {
		return 0, false, errNoSettings
	}
	sealed, err := s.seal(ctx, settingSubject(name, subject), string(value))
	if err != nil {
		return 0, false, err
	}
	return inner.PutSetting(ctx, name, subject, []byte(sealed), match)
}

// Setting reads the person's setting name. A revision of 0 means none is
// kept. A store that cannot be used is ErrUnavailable; a kept value that
// cannot be decrypted matches ErrUnreadable and is returned with its
// revision, so it can be replaced.
func (d *Delegation) Setting(ctx context.Context, name, subject string) ([]byte, uint64, error) {
	settings, err := d.settings()
	if err != nil {
		return nil, 0, err
	}
	value, rev, err := settings.GetSetting(ctx, name, subject)
	if err != nil && !errors.Is(err, ErrUnreadable) {
		return nil, 0, d.unavailable(ctx, err)
	}
	return value, rev, err
}

// PutSetting stores the person's setting name when its revision is still
// match. ok is false when another write came first.
func (d *Delegation) PutSetting(ctx context.Context, name, subject string, value []byte, match uint64) (bool, error) {
	settings, err := d.settings()
	if err != nil {
		return false, err
	}
	_, ok, err := settings.PutSetting(ctx, name, subject, value, match)
	if err != nil {
		return false, d.unavailable(ctx, err)
	}
	return ok, nil
}

// WithSettingLease runs work while the person's lease for setting name is
// held on every replica, so a change and its report to Synaplan are not
// overtaken by another one. It is not the lease of the person's token, so
// it never waits for a token refresh.
func (d *Delegation) WithSettingLease(ctx context.Context, name, subject string, work func(context.Context) error) error {
	if _, err := d.settings(); err != nil {
		return err
	}
	return d.leased(ctx, settingSubject(name, subject), work)
}

func (d *Delegation) settings() (Settings, error) {
	if d == nil || d.cfg.Store == nil {
		return nil, errNoSettings
	}
	settings, ok := d.cfg.Store.(Settings)
	if !ok {
		return nil, errNoSettings
	}
	return settings, nil
}
