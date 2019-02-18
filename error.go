package tlog

import (
	"net/http"

	"github.com/getsentry/raven-go"
)

type LogError struct {
	_error       error
	_err_code    string
	_err_msg     []string
	_stack_track *raven.Stacktrace
	_request     *http.Request
}

func NewLogError(err error, code, msg string) *LogError {
	return &LogError{
		_error:       err,
		_err_code:    code,
		_err_msg:     []string{msg},
		_stack_track: raven.NewStacktrace(1, 3, []string{}),
	}
}

func (this *LogError) AttachRequest(request *http.Request) *LogError {
	this._request = request

	return this
}

func (this *LogError) Error() error {
	return this._error
}

func (this *LogError) ErrCode() string {
	return this._err_code
}

func (this *LogError) AttachErrMsg(msg string) *LogError {
	this._err_msg = append(this._err_msg, msg)

	return this
}
