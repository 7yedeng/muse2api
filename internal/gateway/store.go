package gateway

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type store struct {
	root string
	aead cipher.AEAD
}

var sessionAAD = []byte("muse2api/session/v1")

func newStore(c Config) (*store, error) {
	root := c.privateDir()
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, errors.New("cannot create private storage")
	}
	if err := protectDirectory(root); err != nil {
		return nil, errors.New("cannot restrict private storage permissions")
	}
	block, err := aes.NewCipher(c.SessionKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &store{root, aead}, nil
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *store) saveSession(cookies map[string]string) error {
	plain, err := json.Marshal(cookies)
	if err != nil {
		return err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	sealed := s.aead.Seal(nonce, nonce, plain, sessionAAD)
	return atomicWrite(filepath.Join(s.root, "session.enc"), sealed)
}

func (s *store) loadSession() (map[string]string, bool, error) {
	data, err := os.ReadFile(filepath.Join(s.root, "session.enc"))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, errors.New("cannot read encrypted session")
	}
	if len(data) < s.aead.NonceSize() {
		return nil, false, errors.New("encrypted session is invalid")
	}
	plain, err := s.aead.Open(nil, data[:s.aead.NonceSize()], data[s.aead.NonceSize():], sessionAAD)
	if err != nil {
		return nil, false, errors.New("encrypted session cannot be decrypted; check the original session key")
	}
	var cookies map[string]string
	if json.Unmarshal(plain, &cookies) != nil || validateCookies(cookies) != nil {
		return nil, false, errors.New("encrypted session is invalid")
	}
	return cookies, true, nil
}
