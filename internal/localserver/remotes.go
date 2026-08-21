package localserver

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Remote represents an enrolled remote bridge server.
type Remote struct {
	Name string `yaml:"name"`
	Host string `yaml:"host"`
}

type remotesFile struct {
	Remotes []Remote `yaml:"remotes"`
}

// RemotesPath returns the path to the remotes registry file.
func RemotesPath(stateDir string) string {
	return filepath.Join(stateDir, "remotes.yaml")
}

// LoadRemotes reads the remotes registry from disk.
// Returns an empty slice if the file does not exist.
func LoadRemotes(stateDir string) ([]Remote, error) {
	data, err := os.ReadFile(RemotesPath(stateDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rf remotesFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		return nil, err
	}
	return rf.Remotes, nil
}

// AddRemote appends a remote to the registry if no entry with the same
// host already exists. Creates the file if it does not exist.
func AddRemote(stateDir, name, host string) error {
	remotes, err := LoadRemotes(stateDir)
	if err != nil {
		return err
	}
	for _, r := range remotes {
		if r.Host == host {
			return nil // already registered
		}
	}
	remotes = append(remotes, Remote{Name: name, Host: host})

	data, err := yaml.Marshal(remotesFile{Remotes: remotes})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(RemotesPath(stateDir), data, 0o644)
}
