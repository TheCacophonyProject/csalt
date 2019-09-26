# csalt

[![Status](https://api.travis-ci.org/TheCacophonyProject/csalt.svg)](https://travis-ci.org/TheCacophonyProject/csalt)

Salt wrapper for translating between friendly names to automated names

## License

This project is licensed under the Apache License 2.0
(https://www.apache.org/licenses/LICENSE-2.0).

## Usage

csalt accepts the following parameters:
1. Device and Groups. A list of Devices or group names to translate separated by a space
	- Devices must be in the format of groupname:devicename
	- Groups will be translated into all devices in this group
2. Salt commands to run e.g. `state.apply test=True`, this can be 1 or multiple commands

If only 1 parameter is supplied this will run directly on salt

## Examples

`csalt "group1 group2:gp" test.ping`

Will run test.ping on all devices in group1 and on device gp in group2.
If multiple devices around found `salt -L` will be run

`csalt test.ping`

will translate to:

`salt test.ping`