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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"path"
	"time"
)

const (
	httpTimeout = 60 * time.Second
	timeout     = 30 * time.Second
	apiBasePath = "/api/v1"
	authUserURL = "/authenticate_user"
	TestAPIHost = "api-test.cacophony.org.nz"
	ProdAPIHost = "api.cacophony.org.nz"

	ShortTTL  = "short"
	MediumTTL = "medium"
	LongTTL   = "long"
)

type CacophonyUserAPI struct {
	username      string
	httpClient    *http.Client
	serverURL     string
	token         string
	authenticated bool
	Debug         bool
}

// joinURL creates an absolute URL with supplied baseURL, and all paths
func joinURL(baseURL string, paths ...string) string {

	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	url := path.Join(paths...)
	u.Path = path.Join(u.Path, url)
	return u.String()
}

func New(serverURL, username, token string) *CacophonyUserAPI {
	api := &CacophonyUserAPI{
		token:      token,
		serverURL:  serverURL,
		username:   username,
		httpClient: newHTTPClient(),
	}
	return api
}

func NewFromConfig(conf *Config) *CacophonyUserAPI {
	api := &CacophonyUserAPI{
		token:      conf.Token,
		serverURL:  conf.ServerURL,
		username:   conf.UserName,
		httpClient: newHTTPClient(),
	}
	return api
}

func (api *CacophonyUserAPI) ServerURL() string {
	return api.serverURL
}

func (api *CacophonyUserAPI) authURL() string {
	return joinURL(api.serverURL, authUserURL)

}

type Device struct {
	GroupName  string `json:"groupname"`
	DeviceName string `json:"devicename"`
	SaltId     int    `json:"saltId"`
}

type DeviceResponse struct {
	Messages   []string `json:"messages"`
	Devices    []Device `json:"devices"`
	StatusCode int      `json:"statusCode"`
}

func (api *CacophonyUserAPI) User() string {
	return api.username
}
func (api *CacophonyUserAPI) HasToken() bool {
	return api.token != ""
}
func (api *CacophonyUserAPI) Authenticated() bool {
	return api.authenticated
}

type tokenResponse struct {
	Messages []string
	Token    string
	ID       int
}

func (api *CacophonyUserAPI) Authenticate(password string) error {
	if password == "" {
		return errors.New("empty password")
	}

	data := map[string]interface{}{
		"username": api.username,
		"password": password,
	}

	payload, err := json.Marshal(data)

	if err != nil {
		return err
	}
	if api.Debug {
		fmt.Printf("Authenticate %v for user %v\n", api.authURL(), api.username)
	}
	postResp, err := api.httpClient.Post(
		api.authURL(),
		"application/json",
		bytes.NewReader(payload),
	)
	if err != nil {
		return err
	}
	defer postResp.Body.Close()

	if err := handleHTTPResponse(postResp); err != nil {
		return err
	}

	var resp tokenResponse
	d := json.NewDecoder(postResp.Body)
	if err := d.Decode(&resp); err != nil {
		return fmt.Errorf("decode: %v", err)
	}
	api.token = resp.Token
	api.authenticated = true
	return nil
}

// SaveTemporaryToken requests a temporary token with read access to devices from the API server
func (api *CacophonyUserAPI) SaveTemporaryToken(ttl string) error {
	if api.token == "" {
		return errors.New("No Token found")
	}
	data := map[string]interface{}{
		"ttl":    ttl,
		"access": map[string]string{"devices": "r"},
	}

	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", joinURL(api.serverURL, "/token"),
		bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", api.token)
	postResp, err := api.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer postResp.Body.Close()
	if err := handleHTTPResponse(postResp); err != nil {
		return err
	}

	var resp tokenResponse
	d := json.NewDecoder(postResp.Body)
	if err := d.Decode(&resp); err != nil {
		return fmt.Errorf("decode: %v", err)
	}

	err = saveTokenConfig("JWT "+resp.Token, api.username)
	return nil
}

func (api *CacophonyUserAPI) GetDevices(groups []string, devices []Device) (*DeviceResponse, error) {
	if api.token == "" {
		return nil, &Error{
			message:        "No Token Supplied",
			authentication: true,
		}
	}
	req, err := http.NewRequest("GET", joinURL(api.serverURL, apiBasePath, "/devices"), nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", api.token)
	resp, err := api.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := handleHTTPResponse(resp); err != nil {
		return nil, err
	}
	var devResp DeviceResponse
	d := json.NewDecoder(resp.Body)
	if err := d.Decode(&devResp); err != nil {
		return nil, fmt.Errorf("decode: %v", err)
	}

	api.authenticated = true
	return &devResp, nil
}

// newHTTPClient initializes and returns a http.Client with default settings
func newHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   timeout,
				KeepAlive: 30 * time.Second,
				DualStack: true,
			}).DialContext,

			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
			ExpectContinueTimeout: 1 * time.Second,
			MaxIdleConns:          5,
			IdleConnTimeout:       90 * time.Second,
		},
	}
}

// handleHTTPResponse checks StatusCode of a response for success and returns an http error
// described in error.go
func handleHTTPResponse(resp *http.Response) error {
	if isAuthorizationError(resp.StatusCode) {
		return &Error{
			message:        fmt.Sprintf("API authentication failed (%d):", resp.StatusCode),
			authentication: true,
		}
	} else if !(isHTTPSuccess(resp.StatusCode)) {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return temporaryError(fmt.Errorf("request failed (%d) and body read failed: %v", resp.StatusCode, err))
		}
		return &Error{
			message:   fmt.Sprintf("HTTP request failed (%d): %s", resp.StatusCode, body),
			permanent: isHTTPClientError(resp.StatusCode),
		}
	}
	return nil
}
func isHTTPSuccess(code int) bool {
	return code >= 200 && code < 300
}

func isAuthorizationError(code int) bool {
	return code == 401
}

func isHTTPClientError(code int) bool {
	return code >= 400 && code < 500
}
