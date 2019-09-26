// csalt - Wrapper for salt.
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

package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/howeyc/gopass"

	"github.com/TheCacophonyProject/csalt/userapi"
	"github.com/alexflint/go-arg"
)

const (
	maxPasswordAttempts = 3
)

var debug = false

type DeviceQuery struct {
	devices []userapi.Device
	groups  []string
	rawArg  string
}

func (devQ *DeviceQuery) RawQuery() bool {
	return len(devQ.rawArg) > 0
}

func (devQ *DeviceQuery) HasValues() bool {
	return len(devQ.devices) > 0 || len(devQ.groups) > 0
}

// UnmarshalText is called automatically by go-arg when an argument of type DeviceQuery is being parsed.
// parses supplied bytes into devices and groups by splitting supplied bytes by spaces.
// Devices must be in the format groupname:devicename
// Groups must be in the format groupname(: optional)
func (devQ *DeviceQuery) UnmarshalText(b []byte) error {
	devQ.rawArg = string(b)
	devices := strings.Split(strings.TrimSpace(string(b)), " ")

	for _, devInfo := range devices {
		pos := strings.Index(devInfo, ":")
		if pos == 0 {
			return errors.New("Groupname is required for devices")
		} else if pos >= 0 {
			if len(devInfo) == pos+1 {
				devQ.groups = append(devQ.groups, devInfo[:pos])
			} else {
				devQ.devices = append(devQ.devices, userapi.Device{
					GroupName:  devInfo[:pos],
					DeviceName: devInfo[pos+1:]})
			}
		} else {
			devQ.groups = append(devQ.groups, devInfo)
		}
	}
	return nil
}

type Args struct {
	DeviceInfo DeviceQuery `arg:"positional"`
	Commands   []string    `arg:"positional"`
	Debug      bool        `arg:"-d" help:"debug"`
}

func procArgs() Args {
	var args Args
	arg.MustParse(&args)
	return args
}

func main() {
	err := runMain()
	if err != nil {
		log.Fatal(err)
	}
}

// authenticateUser checks user authentication and requests user password if required
// once authenticated requests and saves a temporary access token
func authenticateUser(api *userapi.CacophonyUserAPI) error {
	if !api.Authenticated() {
		err := requestAuthentication(api)
		if err != nil {
			return err
		}
	}
	return api.SaveTemporaryToken(userapi.LongTTL)
}

// requestAuthentication requests a password from the user and checks it against the API server,
func requestAuthentication(api *userapi.CacophonyUserAPI) error {
	attempts := 0
	fmt.Printf("Authentication is required for %v\n", api.User())
	fmt.Print("Enter Password: ")
	for !api.Authenticated() {
		bytePassword, err := gopass.GetPasswd()
		if err != nil {
			return err
		}
		err = api.Authenticate(string(bytePassword))
		if err == nil {
			break
		} else if !userapi.IsAuthenticationError(err) {
			return err
		}
		attempts += 1
		if attempts == maxPasswordAttempts {
			return errors.New("Max Password Attempts")
		}
		fmt.Print("\nIncorrect user/password try again\nEnter Password: ")
	}
	return nil
}

// getMissingConfig from the user and save to config file
func getMissingConfig(conf *userapi.Config) {
	fmt.Println("User configuration missing")
	if conf.ServerURL == "" {
		fmt.Print("Enter API ServerURL: ")
		fmt.Scanln(&conf.ServerURL)
	}

	if conf.UserName == "" {
		fmt.Print("Enter Username: ")
		fmt.Scanln(&conf.UserName)
	}
}

func getSaltPrefix(serverURL string) string {
	idPrefix := "pi"
	url, err := url.Parse(serverURL)
	if err != nil {
		fmt.Printf("Error parsing serverURL %v\n", err)
		return idPrefix
	}
	if url.Host == userapi.TestAPIHost {
		idPrefix += "-test"
	}
	return idPrefix
}

// saltDeviceCommand adds a prefix to all supplied devices based on the server and returns
// a quoted string of device names separated by a space
func saltDeviceCommand(serverURL string, devices []userapi.Device) string {
	var saltDevices bytes.Buffer
	idPrefix := getSaltPrefix(serverURL)
	spacer := ""
	for _, device := range devices {
		saltDevices.WriteString(spacer + idPrefix + "-" + strconv.Itoa(device.SaltId))
		spacer = " "
	}
	return saltDevices.String()
}

// runSaltForDevices executes salt on supplied devices with argCommands
func runSaltForDevices(serverURL string, devices []userapi.Device, argCommands []string) error {
	if len(devices) == 0 {
		return errors.New("No valid devices found")
	}
	ids := saltDeviceCommand(serverURL, devices)
	commands := make([]string, 0, 6)
	if len(devices) > 1 {
		commands = append(commands, "-L")
	}
	commands = append(commands, ids)
	commands = append(commands, argCommands...)
	return runSalt(commands...)
}

// runSalt with sudo on supplied arguments
func runSalt(commands ...string) error {

	commands = append([]string{"salt"}, commands...)
	if debug {
		fmt.Printf("sudo %v\n", strings.Join(commands, " "))
	}
	cmd := exec.Command("sudo", commands...)
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	err := cmd.Run()
	return err
}

func runMain() error {
	args := procArgs()
	debug = args.Debug
	if len(args.Commands) == 0 {
		if args.DeviceInfo.RawQuery() {
			return runSalt(args.DeviceInfo.rawArg)
		}
		return errors.New("A command must be specified")
	} else if !args.DeviceInfo.HasValues() {
		return runSalt(args.Commands...)
	}

	config, err := userapi.NewConfig()
	if err != nil {
		getMissingConfig(config)
		err = config.Save()
		if err != nil {
			fmt.Printf("Error saving config %v", err)
		}
	}

	api := userapi.New(config)
	api.Debug = debug
	if !api.HasToken() {
		err = authenticateUser(api)
		if err != nil {
			return err
		}
	}

	devices, err := api.TranslateNames(args.DeviceInfo.groups, args.DeviceInfo.devices)
	if userapi.IsAuthenticationError(err) {
		err = authenticateUser(api)

		if err != nil {
			return err
		}
		devices, err = api.TranslateNames(args.DeviceInfo.groups, args.DeviceInfo.devices)

	}

	if err != nil {
		return err
	}

	return runSaltForDevices(api.ServerURL(), devices, args.Commands)
}
