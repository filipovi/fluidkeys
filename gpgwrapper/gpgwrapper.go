// gpgwrapper calls out to the system GnuPG binary

package gpgwrapper

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
)

const GpgPath = "gpg"

var ErrNoVersionStringFound = errors.New("version string not found in GPG output")

func ErrProblemExecutingGPG(arguments ...string) error {
	return fmt.Errorf("problem executing GPG with %s", arguments)
}

var VersionRegexp = regexp.MustCompile(`gpg \(GnuPG.*\) (\d+\.\d+\.\d+)`)

func Version() (string, error) {
	// Returns the GnuPG version string, e.g. "1.2.3"

	outString, err := runGpg("--version")

	if err != nil {
		err = fmt.Errorf("problem running GPG, %v", err)
		return "", err
	}

	version, err := parseVersionString(outString)

	if err != nil {
		err = fmt.Errorf("problem parsing version string, %v", err)
		return "", err
	}

	return version, nil
}

func parseVersionString(gpgStdout string) (string, error) {
	match := VersionRegexp.FindStringSubmatch(gpgStdout)

	if match == nil {
		return "", ErrNoVersionStringFound
	}

	return match[1], nil
}

func runGpg(arguments ...string) (string, error) {
	out, err := exec.Command(GpgPath, arguments...).Output()

	if err != nil {
		// TODO: it would be kinder if we interpreted GPG's
		// output and returned a specific Error type.

		err = ErrProblemExecutingGPG(arguments...)
		return "", err
	}
	outString := string(out)
	return outString, nil
}

func runGpgWithStdin(textToSend string, arguments ...string) (string, error) {

	cmd := exec.Command(GpgPath, arguments...)
	stdin, err := cmd.StdinPipe()

	if err != nil {
		return "", errors.New(fmt.Sprintf("Failed to get stdin pipe '%s'", err))
	}

	io.WriteString(stdin, textToSend)
	stdin.Close()

	stdoutAndStderr, err := cmd.CombinedOutput()

	if err != nil {
		return "", errors.New(fmt.Sprintf("GPG failed with error '%s', stdout said '%s'", err, stdoutAndStderr))
	}

	output := string(stdoutAndStderr)
	return output, nil
}
