# csalt

[![Status](https://api.travis-ci.org/TheCacophonyProject/csalt.svg)](https://travis-ci.org/TheCacophonyProject/csalt)

Salt wrapper for translating between friendly names to automated names

## License

This project is licensed under the Apache License 2.0
(https://www.apache.org/licenses/LICENSE-2.0).

## Usage


```
usage: csalt [-s] [--server SERVER] [--user USER]
                        [--test] [--live] [-t] [-d]
                        DEVICEINFO COMMANDS

positional arguments:
  DEVICEINFO            Comma separated list of device/group names to translate salt ids on (See below for more details)
  COMMANDS              Salt commands to run on these devices e.g. `state.apply test=True`, this can be 1 or multiple commands

optional arguments:
  -h, --help            Show this help message and exit
  -s --show             Print salt ids for device names, this will override
  --server SERVER
                        Use server configuration for the specified server alias in cacophony-user.yaml
                        servers:
                          <SERVER ALIAS>:
                            url: http://127.0.0.1:1080/
                            salt-prefix: test
                            user-name: admin_test
  --user USER           Use this user name to authenticate with, this will be saved to cacophony-user.yaml
  --test                Connect to test api server https://api-test.cacophony.org.nz/
  --live                Connect to live api server https://api.cacophony.org.nz/
  -t --test-prefix      Append test to salt ids e.g. pi-test-XXX
  -d --debug            Enable debug mode with extra logging
  -v --verbose          Enables more verbose output
```

DEVICEINFO:
1. Device and Groups. A list of Devices or group names to translate separated by a comma
	- Devices can be in the format of groupname:devicename, or :devicename (which will match any group)
	- Groups will be translated into all devices in this group

If only 1 parameter is supplied this will run directly on salt

Once a user has been authenticated a temporary token will be saved to /home/user/.cacophony-token

## Config
/home/user/cacophony-user.yaml

```
user-name: Delaley
server-url: https://api-beta.cacophony.org.nz/
servers:
  local:
    url: http://127.0.0.1:1080/
    salt-prefix: test
    user-name: roger_test
  alpha:
    url: http://192.168.1.102:1080/
    salt-prefix: alpha
```

## Examples

- Argument Examples:
More specific information will override, other information.

If no server-url is specified, csalt will use the live server.
live, test,test-prefix and user arguments will override others

With the above config by default

`csalt "group1" -s`

will run on https://api-beta.cacophony.org.nz/ with user-name Delaley

Speccifying local server

`csalt "group1" -s --server local`

will run on http://127.0.0.1:1080/ with user-name roger_test

Sepcifying alpha server the username will fall back to the default

`csalt "group1" -s --server alpha`

will run on http://127.0.0.1:1080/ with user-name Delaley

Sepcifying alpha server the username will fall back to the default

`csalt "group1" -s --server local  --user overloard`

will run on http://127.0.0.1:1080/ with user-name overlord

- DeviceInfo examples:

`csalt "group1,group2:gp" test.ping`

Will run test.ping on all devices in group1 and on device gp in group2.
If multiple devices around found `salt -L` will be run

`csalt ":gp" test.ping`

Will find all devices named gp of any group and run test.ping

`csalt ":gp" -s`

Will find all devices named gp and print out there salt ids

`csalt test.ping`

will translate to:

`salt test.ping`