package main

import (
	"errors"
)

type CommandErrorCode string

const (
	CommandErrNotFound              CommandErrorCode = "not_found"
	CommandErrTimeout               CommandErrorCode = "timeout"
	CommandErrProjectionUnavailable CommandErrorCode = "projection_unavailable"
	CommandErrInvalidPayload        CommandErrorCode = "invalid_payload"
	CommandErrUnsupportedAction     CommandErrorCode = "unsupported_action"
)

type CommandError struct {
	Code CommandErrorCode
	Msg  string
	Err  error
}

func (e *CommandError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Code)
}

func (e *CommandError) Unwrap() error {
	return e.Err
}

func commandErr(code CommandErrorCode, msg string, err error) error {
	return &CommandError{Code: code, Msg: msg, Err: err}
}

func commandErrCode(err error) (CommandErrorCode, bool) {
	var ce *CommandError
	if errors.As(err, &ce) {
		return ce.Code, true
	}
	return "", false
}
