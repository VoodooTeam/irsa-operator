package controllers

import (
	"fmt"
)

type completed bool

// Helper functions to check and remove string from a slice of strings.
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}

func newMsg(msg string) Event {
	return Event{
		msg: msg,
	}
}

func newErr(msg string, err error) Event {
	return Event{
		msg: msg,
		err: err,
	}
}

type Event struct {
	msg string
	err error
}

func (e Event) String() string {
	if e.err != nil {
		return fmt.Sprintf("%s (%s)", e.msg, e.err)
	}
	return e.msg
}

func scope(sc string) func(string) string {
	return func(s string) string {
		return sc + ": " + s
	}
}
