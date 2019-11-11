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
	testPrefix          = "test"
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
	devices := strings.Split(strings.TrimSpace(string(b)), ",")

	for _, devInfo := range devices {
		pos := strings.Index(devInfo, ":")
		if pos == 0 {
			devQ.devices = append(devQ.devices, userapi.Device{
				DeviceName: devInfo[1:]})
		} else if pos >= 0 {
			if len(devInfo) == pos+1 {
				devQ.groups = append(devQ.groups, devInfo[:pos])
			} else {
				devQ.devices = append(devQ.devices, userapi.Device{
					GroupName:  devInfo[:pos],
					DeviceName: devInfo[pos+1:]})
			}
		} else {
			devQ.devices = append(devQ.devices, userapi.Device{
				DeviceName: devInfo})
		}
	}
	return nil
}

func (Args) Description() string {
	return `DEVICEINFO:
1. Device and Groups. A list of Devices or group names to translate separated by a comma
	- Devices can be in the format of groupname:devicename, or devicename (which will match any group)
	- Groups will be translated into all devices in this group groupname:

If only 1 parameter is supplied this will run directly on salt

Once a user has been authenticated a temporary token will be saved to /home/user/.cacophony-token

Examples:
csalt "gp" test.ping
Will find all devices named gp of any group and run test.ping

csalt "group1:,group2:gp" test.ping
Will run test.ping on all devices in group1 and on device gp in group2.`
}

type Args struct {
	DeviceInfo DeviceQuery `arg:"positional"`
	Commands   []string    `arg:"positional"`
	Show       bool        `arg:"-s" help:"Print salt ids for device names"`
	Server     string      `help:"--server to use, this should be defined in cacophony-user.yaml"`
	TestServer bool        `arg:"--test" help:"Connect to the test api server"`
	LiveServer bool        `arg:"--live" help:"Connect to the live api server"`
	TestPrefix bool        `arg:"-t" help:"Add -test to salt names e.g. pi-test-xxx"`
	User       string      `arg:"--user" help:"Username to authenticate with server"`
	Debug      bool        `arg:"-d" help:"debug"`
	Verbose    bool        `arg:"-v" help:"verbose"`
}

func procArgs() Args {
	var args Args
	arg.MustParse(&args)
	if args.Verbose {
		for _, device := range args.DeviceInfo.devices {
			if device.GroupName == "" {
				fmt.Printf("Looking for device by name %v\n", device.DeviceName)
			} else {
				fmt.Printf("Looking for group:device %v:%v\n", device.GroupName, device.DeviceName)
			}
		}
		for _, group := range args.DeviceInfo.groups {
			fmt.Printf("Looking for devices in group %v\n", group)

		}
	}
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

	if conf.UserName == "" {
		fmt.Print("Enter Username: ")
		fmt.Scanln(&conf.UserName)
	}
}

func getSaltPrefix(serverURL, saltPrefix string) string {
	idPrefix := "pi"
	if saltPrefix != "" {
		idPrefix += "-" + saltPrefix
	}
	return idPrefix
}

// saltDeviceCommand adds a prefix to all supplied devices based on the server and returns
// a quoted string of device names separated by a space
func saltDeviceCommand(serverURL string, devices []userapi.Device, saltPrefix string) string {
	var saltDevices bytes.Buffer
	idPrefix := getSaltPrefix(serverURL, saltPrefix)
	spacer := ""
	for _, device := range devices {
		saltDevices.WriteString(spacer + idPrefix + "-" + strconv.Itoa(device.SaltId))
		spacer = " "
	}
	return saltDevices.String()
}

// runSaltForDevices executes salt on supplied devices with argCommands
func runSaltForDevices(serverURL string, devices []userapi.Device, argCommands []string, saltPrefix string) error {
	if len(devices) == 0 {
		return errors.New("No valid devices found")
	}
	ids := saltDeviceCommand(serverURL, devices, saltPrefix)
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

func apiFromArgs(args Args) (*userapi.CacophonyUserAPI, string, error) {
	config, _ := userapi.NewConfig()
	serverURL := config.ServerURL
	var saltPrefix, username string
	if args.LiveServer {
		serverURL = fmt.Sprintf("https://%v", userapi.LiveAPIHost)
	} else if args.TestServer {
		serverURL = fmt.Sprintf("https://%v", userapi.TestAPIHost)
		saltPrefix = testPrefix
	} else if args.Server != "" {
		if server, ok := config.Servers[args.Server]; ok {
			serverURL = server.Url
			saltPrefix = server.SaltPrefix
			username = server.UserName
		} else {
			return nil, "", fmt.Errorf("Cannot find %v server info in config", args.Server)
		}
	} else if serverURL == "" {
		serverURL = fmt.Sprintf("https://%v", userapi.LiveAPIHost)
	}

	if args.TestPrefix {
		saltPrefix = testPrefix
	}
	if args.User != "" {
		username = args.User
	} else if username == "" {
		if config.UserName == "" {
			getMissingConfig(config)
			err := config.Save()
			if err != nil {
				fmt.Printf("Error saving config %v\n", err)
			}
		}
		username = config.UserName
	}

	token, err := userapi.ReadTokenFor(username)
	if args.Debug && err != nil {
		fmt.Printf("ReadToken error %v\n", err)
	}
	api := userapi.New(serverURL, username, token)
	return api, saltPrefix, nil
}

func checkForDuplicates(devices *userapi.DeviceResponse) error {
	nameMap := make(map[string][]userapi.Device)
	duplicateNames := make([]string, 0, 1)
	for _, device := range devices.NameMatches {
		if _, ok := nameMap[device.DeviceName]; !ok {
			nameMap[device.DeviceName] = []userapi.Device{device}
		} else {
			nameMap[device.DeviceName] = append(nameMap[device.DeviceName], device)
			duplicateNames = append(duplicateNames, device.DeviceName)
		}
	}
	if len(duplicateNames) > 0 {
		fmt.Printf("DeviceName Query found %v Duplicate Device please specify group:devicename\n", len(duplicateNames))
		for _, name := range duplicateNames {
			fmt.Printf("Device %v matches:\n", name)
			for _, device := range nameMap[name] {
				fmt.Printf("%v:%v\n", device.GroupName, device.DeviceName)
			}
		}
		return fmt.Errorf("DeviceName Query found %v Duplicate Device please specify group:devicename\n", len(duplicateNames))
	}
	return nil
}

func showTranslatedDvices(devices *userapi.DeviceResponse) {

	fmt.Println("Translated Name matches:")
	for _, device := range devices.NameMatches {
		fmt.Printf("%v:%v saltid: %v\n", device.GroupName, device.DeviceName, device.SaltId)
	}

	fmt.Println("Translated Devices:")
	for _, device := range devices.Devices {
		fmt.Printf("%v:%v saltid: %v\n", device.GroupName, device.DeviceName, device.SaltId)
	}

}
func runMain() error {
	args := procArgs()
	debug = args.Debug

	if len(args.Commands) == 0 {
		if args.DeviceInfo.RawQuery() {
			if !args.Show {
				return runSalt(args.DeviceInfo.rawArg)
			}
		} else {
			return errors.New("Commands/deviceinfo must be specified")
		}
	} else if !args.DeviceInfo.HasValues() {
		return runSalt(args.Commands...)
	}
	api, saltPrefix, err := apiFromArgs(args)
	if err != nil {
		return err
	}

	if args.Debug {
		fmt.Printf("CSalt using server %v, saltprefix %v, user %v\n", api.ServerURL(), saltPrefix, api.User())
	}
	api.Debug = debug
	if !api.HasToken() {
		err = authenticateUser(api)
		if err != nil {
			return err
		}
	}

	devResp, err := api.TranslateNames(args.DeviceInfo.groups, args.DeviceInfo.devices)
	if userapi.IsAuthenticationError(err) {
		err = authenticateUser(api)

		if err != nil {
			return err
		}
		devResp, err = api.TranslateNames(args.DeviceInfo.groups, args.DeviceInfo.devices)

	}
	if err != nil {
		return err
	}

	if args.Verbose {
		showTranslatedDvices(devResp)
	}

	err = checkForDuplicates(devResp)
	if err != nil {
		return err
	}
	allDevices := append(devResp.Devices, devResp.NameMatches...)

	if args.Show {
		ids := saltDeviceCommand(api.ServerURL(), allDevices, saltPrefix)
		fmt.Printf("translated salt names %v\n", ids)
	}
	if len(args.Commands) > 0 {
		return runSaltForDevices(api.ServerURL(), allDevices, args.Commands, saltPrefix)
	}
	return nil
}
