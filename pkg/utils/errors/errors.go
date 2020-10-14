package errors

import (
	"fmt"
	"os"

	"github.com/go-logr/logr"
)

const (
	// ErrorCommandSpecific is reserved for command specific indications
	ErrorCommandSpecific = 1
	// ErrorConnectionFailure is returned on connection failure to API endpoint
	ErrorConnectionFailure = 11
	// ErrorAPIResponse is returned on unexpected API response, i.e. authorization failure
	ErrorAPIResponse = 12
	// ErrorResourceDoesNotExist is returned when the requested resource does not exist
	ErrorResourceDoesNotExist = 13
	// ErrorGeneric is returned for generic error
	ErrorGeneric = 20
)

// CheckError logs a fatal message and exits with ErrorGeneric if err is not nil
func CheckError(err error, log logr.Logger) {
	if err != nil {
		Fatal(log, ErrorGeneric, err)
	}
}

// CheckErrorWithCode is a convenience function to exit if an error is non-nil and exit if it was
func CheckErrorWithCode(err error, exitcode int, log logr.Logger) {
	if err != nil {
		Fatal(log, exitcode, err)
	}
}

// FailOnErr terminates the program if there is an error. It returns the first value so you can use it if you cast it:
// text := FailOrErr(Foo)).(string)
func FailOnErr(log logr.Logger, v interface{}, err error) interface{} {
	CheckError(err, log)
	return v
}

// Fatal is a helper to exit with custom code.
func Fatal(log logr.Logger, exitcode int, keysAndValues ...interface{}) {
	log.Error(fmt.Errorf("exit code %d", exitcode), "Fatal error", keysAndValues...)
	os.Exit(exitcode)
}
