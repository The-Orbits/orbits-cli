package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

// KeyringService is the keychain service the tokens are stored under, one
// entry per profile (the account is the profile's name). orbits-mcp reads
// the same entry to use the CLI's sign-in.
const KeyringService = "orbits-cli"

// Where SaveToken put a token.
const (
	InKeychain = "keychain"
	InFile     = "file"
)

// keyringOff is ORBITS_NO_KEYRING: keep tokens out of the keychain, in
// credentials.json, as on a machine without one.
func keyringOff() bool { return os.Getenv("ORBITS_NO_KEYRING") != "" }

// keychain is the OS keychain; a variable so tests can stand in for it.
var keychain interface {
	Set(service, user, password string) error
	Get(service, user string) (string, error)
	Delete(service, user string) error
} = osKeychain{}

type osKeychain struct{}

func (osKeychain) Set(s, u, p string) error        { return keyring.Set(s, u, p) }
func (osKeychain) Get(s, u string) (string, error) { return keyring.Get(s, u) }
func (osKeychain) Delete(s, u string) error        { return keyring.Delete(s, u) }

// SaveToken stores a profile's API token in the OS keychain (the macOS
// Keychain, or the Secret Service on Linux), or, where there is no keychain
// (a headless server), in credentials.json beside the config, readable only
// by you. It says which.
func SaveToken(profile, token string) (string, error) {
	if !keyringOff() {
		if err := keychain.Set(KeyringService, profile, token); err == nil {
			// A token moved into the keychain should not linger in the file.
			_ = deleteFileToken(profile)
			return InKeychain, nil
		}
	}
	return InFile, setFileToken(profile, token)
}

// LoadToken returns a profile's stored token, or "" if it has none. Not in
// the keychain, or no keychain on this machine: then the file is where it went.
func LoadToken(profile string) (string, error) {
	if !keyringOff() {
		if t, err := keychain.Get(KeyringService, profile); err == nil && t != "" {
			return t, nil
		}
	}
	return fileToken(profile)
}

// DeleteToken removes a profile's token from wherever it is. The keychain is
// asked best-effort: "not there" and "no keychain on this machine" read alike.
func DeleteToken(profile string) error {
	if !keyringOff() {
		_ = keychain.Delete(KeyringService, profile)
	}
	return deleteFileToken(profile)
}

// CredentialsPath is the file tokens go in without a keychain.
func CredentialsPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "credentials.json"), nil
}

func readCredentials() (map[string]string, error) {
	p, err := CredentialsPath()
	if err != nil {
		return nil, err
	}
	creds := map[string]string{}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return creds, nil
	}
	if err != nil {
		return nil, err
	}
	return creds, json.Unmarshal(data, &creds)
}

func writeCredentials(creds map[string]string) error {
	p, err := CredentialsPath()
	if err != nil {
		return err
	}
	if len(creds) == 0 {
		err := os.Remove(p)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(p, append(data, '\n'))
}

func fileToken(profile string) (string, error) {
	creds, err := readCredentials()
	if err != nil {
		return "", err
	}
	return creds[profile], nil
}

func setFileToken(profile, token string) error {
	creds, err := readCredentials()
	if err != nil {
		return err
	}
	creds[profile] = token
	return writeCredentials(creds)
}

func deleteFileToken(profile string) error {
	creds, err := readCredentials()
	if err != nil {
		return err
	}
	if _, ok := creds[profile]; !ok {
		return nil
	}
	delete(creds, profile)
	return writeCredentials(creds)
}
