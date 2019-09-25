// userapi - Client for the Cacophony API server.
// Copyright (C) 2018, The Cacophony Project
//
//Licensed under the Apache License, Version 2.0 (the "License");
//you may not use this file except in compliance with the License.
//You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
//Unless required by applicable law or agreed to in writing, software
//distributed under the License is distributed on an "AS IS" BASIS,
//WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//See the License for the specific language governing permissions and
//limitations under the License.
package userapi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/user"
	"path"
	"time"

	"github.com/gofrs/flock"
	"github.com/spf13/afero"
	"gopkg.in/yaml.v2"
)

const (
	userConfig     = "cacophony-user.yaml"
	tokenFileName  = ".cacophony-token"
	lockRetryDelay = 678 * time.Millisecond
	lockTimeout    = 5 * time.Second
)

type Config struct {
	ServerURL string `yaml:"server-url"`
	UserName  string `yaml:"user-name"`
	token     string
	filePath  string
}

func userHomeDir() string {
	usr, err := user.Current()
	if err != nil {
		log.Fatal(err)
	}
	return usr.HomeDir
}

func NewConfig() (*Config, error) {
	homeDir := userHomeDir()
	filePath := path.Join(homeDir, userConfig)
	conf := &Config{filePath: filePath}
	tokenConfig, err := readTokenConfig()
	if err != nil {
		fmt.Printf("error loading token%v", err)
	}

	if exists, err := afero.Exists(Fs, filePath); err != nil {
		return conf, err
	} else if !exists {
		return conf, errors.New("user config is missing")
	}

	err = conf.read()
	if conf.UserName == tokenConfig.UserName {
		conf.token = tokenConfig.Token
	}

	if err != nil {
		return conf, err
	}
	if err := conf.Validate(); err != nil {
		return conf, err
	}

	return conf, nil
}

func (c *Config) read() error {
	if exists, err := afero.Exists(Fs, c.filePath); err != nil {
		return err
	} else if !exists {
		return errors.New("User config is missing")
	}

	lockSafeConfig := NewLockSafeConfig(c.filePath)
	bytes, err := lockSafeConfig.Read()
	if err != nil {
		return err
	}
	return yaml.Unmarshal(bytes, c)
}

func (c *Config) Save() error {
	lockSafeConfig := NewLockSafeConfig(c.filePath)
	_, err := lockSafeConfig.ExLock()
	if err != nil {
		return err
	}
	buf, err := yaml.Marshal(&c)
	if err != nil {
		return err
	}
	return lockSafeConfig.Write(buf)
}

//Validate checks supplied Config contains the required data
func (conf *Config) Validate() error {
	if conf.ServerURL == "" {
		return errors.New("server-url missing")
	}

	if conf.UserName == "" {
		return errors.New("user-name is missing")
	}
	return nil
}

type TokenConfig struct {
	UserName string `yaml:"user-name"`
	Token    string `yaml:"token"`
}

// readTokenConfig acquires a readlock and reads token config
func readTokenConfig() (*TokenConfig, error) {
	tokenPath := path.Join(userHomeDir(), tokenFileName)
	config := &TokenConfig{}
	lockSafeConfig := NewLockSafeConfig(tokenPath)
	bytes, err := lockSafeConfig.Read()
	if err != nil {
		return config, err
	}
	err = yaml.Unmarshal(bytes, config)
	return config, err
}

// readTokenConfig acquires a exlock and saves token config
func saveTokenConfig(token, username string) error {
	tokenPath := path.Join(userHomeDir(), tokenFileName)
	lockSafeConfig := NewLockSafeConfig(tokenPath)
	_, err := lockSafeConfig.ExLock()
	if err != nil {
		return err
	}
	tokenConfig := &TokenConfig{UserName: username, Token: token}
	buf, err := yaml.Marshal(&tokenConfig)
	if err != nil {
		return err
	}
	return lockSafeConfig.Write(buf)
}

type LockSafeConfig struct {
	fileLock *flock.Flock
	filename string
	token    string
}

func NewLockSafeConfig(filename string) *LockSafeConfig {
	lockFile := filename + ".lock"
	return &LockSafeConfig{
		filename: filename,
		fileLock: flock.New(lockFile),
	}
}

func (lockSafeConfig *LockSafeConfig) Unlock() {
	lockSafeConfig.fileLock.Unlock()
}

// ExLock acquires an exclusive lock on confPassword
func (lockSafeConfig *LockSafeConfig) ExLock() (bool, error) {
	lockCtx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()
	locked, err := lockSafeConfig.fileLock.TryLockContext(lockCtx, lockRetryDelay)
	return locked, err
}

// ReadPassword acquires a read lock and reads the config
func (lockSafeConfig *LockSafeConfig) Read() ([]byte, error) {
	locked := lockSafeConfig.fileLock.Locked()
	if locked == false {
		locked, err := readLock(lockSafeConfig.fileLock)
		if locked == false || err != nil {
			return nil, err
		}
		defer lockSafeConfig.Unlock()
	}

	buf, err := afero.ReadFile(Fs, lockSafeConfig.filename)
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	return buf, nil
}

// readLock  acquires a read lock on the supplied Flock struct
func readLock(fileLock *flock.Flock) (bool, error) {
	lockCtx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()
	locked, err := fileLock.TryRLockContext(lockCtx, lockRetryDelay)
	return locked, err
}

// Write supplied data to exclusively locked file
func (lockSafeConfig *LockSafeConfig) Write(data []byte) error {
	if lockSafeConfig.fileLock.Locked() {
		err := afero.WriteFile(Fs, lockSafeConfig.filename, data, 0600)
		return err
	} else {
		return fmt.Errorf("file is not locked %v", lockSafeConfig.filename)
	}
}

var Fs = afero.NewOsFs()
