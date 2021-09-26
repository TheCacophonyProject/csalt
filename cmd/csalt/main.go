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
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/howeyc/gopass"
	"gopkg.in/yaml.v1"

	"github.com/TheCacophonyProject/csalt/userapi"
	"github.com/alexflint/go-arg"
)

const (
	maxPasswordAttempts = 3
	testPrefix          = "test"
	nodeGroupFile       = "/etc/salt/master.d/nodegroups.conf"
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
	ProdServer bool        `arg:"--prod" help:"Connect to the prod api server"`
	TestPrefix bool        `arg:"-t" help:"Add -test to salt names e.g. pi-test-xxx"`
	NoPrefix     bool      `arg:"--no-prefix" help:"Dont add a prefix even if test"`
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
func saltDeviceCommand(serverURL string, devices []userapi.Device, saltPrefix string) []string {
	idPrefix := getSaltPrefix(serverURL, saltPrefix)
	fullDevice := make([]string, len(devices))
	for i := 0; i < len(devices); i++ {
		fullDevice[i] = idPrefix + "-" + strconv.Itoa(devices[i].SaltId)
	}
	return fullDevice
}

// runSaltForDevices executes salt on supplied devices with argCommands
func runSaltForDevices(serverURL string, devices []userapi.Device, argCommands []string, saltPrefix string) error {
	if len(devices) == 0 {
		return errors.New("No valid devices found")
	}
	ids := strings.Join(saltDeviceCommand(serverURL, devices, saltPrefix), " ")
	commands := make([]string, 0, 6)
	if len(devices) > 1 {
		commands = append(commands, "-L")
	}
	commands = append(commands, ids)
	commands = append(commands, argCommands...)
	return runSalt(commands...)
}

// getSaltOutput with sudo on supplied arguments
func getSaltOutput(commands ...string) (string, error) {
	commands = append([]string{"salt"}, commands...)
	if debug {
		fmt.Printf("sudo %v\n", strings.Join(commands, " "))
	}
	output, err := exec.Command("sudo", commands...).Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
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
	if args.ProdServer {
		serverURL = fmt.Sprintf("https://%v", userapi.ProdAPIHost)
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
		serverURL = fmt.Sprintf("https://%v", userapi.ProdAPIHost)
	}

	if args.TestPrefix {
		saltPrefix = testPrefix
	}
	if args.NoPrefix {
		saltPrefix = ""
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
		for _, name := range duplicateNames {
			fmt.Printf("Device %v matches:\n", name)
			for _, device := range nameMap[name] {
				fmt.Printf("%v:%v\n", device.GroupName, device.DeviceName)
			}
		}
		return fmt.Errorf("Found %v ambiguous devices. Please specify these devices in full group:devicename form.\n", len(duplicateNames))
	}
	return nil
}

// readNodeFile of salt and return a map of node to nodegroup name
func readNodeFile() map[string][]string {
	//get all the nodegroups
	var nodeYaml map[string]map[string]interface{}
	nodeFile, err := ioutil.ReadFile(nodeGroupFile)
	if err != nil {
		fmt.Printf("readNodeFile, error %v ", err)
	}
	err = yaml.Unmarshal(nodeFile, &nodeYaml)
	if err != nil {
		fmt.Printf("yaml, error %v ", err)
	}

	nodesToGroup := make(map[string][]string)
	commands := []string{"--preview-target", "-N", "group"}
	//easiest way to find all pis that belong to a group is to run salt on the
	//node group with preview-target
	for key, _ := range nodeYaml["nodegroups"] {
		commands[2] = key
		output, err := getSaltOutput(commands...)
		if err != nil {
			fmt.Printf("Error getting node targets for %s, err %v\n", key, err)
			continue
		}
		devices := strings.Split(strings.TrimSpace(output), "\n")
		for i := 0; i < len(devices); i++ {
			deviceName := strings.TrimSpace(devices[i])[2:]
			if _, found := nodesToGroup[deviceName]; !found {
				nodesToGroup[deviceName] = make([]string, 0, 2)
			}
			nodesToGroup[deviceName] = append(nodesToGroup[deviceName], key)
		}
	}
	return nodesToGroup
}

func showTranslatedDevices(devices *userapi.DeviceResponse, saltPrefix string) {
	nodesToGroup := readNodeFile()
	noNodeGroup := make([]userapi.Device, 0, 5)
	fmt.Println("Devices found:")
	for _, device := range devices.NameMatches {
		if nodeGroups, found := nodesToGroup[saltPrefix+"-"+strconv.Itoa(device.SaltId)]; found {
			fmt.Printf("%v:%v saltid: %v nodeGroup %v\n", device.GroupName, device.DeviceName, saltPrefix+"-"+strconv.Itoa(device.SaltId), nodeGroups)
		} else {
			noNodeGroup = append(noNodeGroup, device)
		}
	}
	for _, device := range devices.Devices {
		if nodeGroups, found := nodesToGroup[saltPrefix+"-"+strconv.Itoa(device.SaltId)]; found {
			fmt.Printf("%v:%v saltid: %v nodeGroup %v\n", device.GroupName, device.DeviceName, saltPrefix+"-"+strconv.Itoa(device.SaltId), nodeGroups)
		} else {
			noNodeGroup = append(noNodeGroup, device)
		}
	}
	if len(noNodeGroup) > 0 {
		fmt.Println("\nDevices without any node group (Probably stale):")
	}
	for _, device := range noNodeGroup {
		fmt.Printf("%v:%v saltid: %v\n", device.GroupName, device.DeviceName, saltPrefix+"-"+strconv.Itoa(device.SaltId))
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

	err = checkForDuplicates(devResp)
	if err != nil {
		return err
	}
	allDevices := append(devResp.Devices, devResp.NameMatches...)

	if args.Show || args.Verbose {
		idPrefix := getSaltPrefix(api.ServerURL(), saltPrefix)
		showTranslatedDevices(devResp, idPrefix)
	}
	if len(args.Commands) > 0 {
		return runSaltForDevices(api.ServerURL(), allDevices, args.Commands, saltPrefix)
	}
	return nil
}
