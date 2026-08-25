package internal

import (
	"strings"

	"github.com/AlecAivazis/survey/v2"
)

// AskSelect prompts the user to choose one of the given options
func AskSelect(message string, options []string) (string, error) {
	var choice string
	prompt := &survey.Select{
		Message: message,
		Options: options,
	}
	if err := survey.AskOne(prompt, &choice); err != nil {
		return "", err
	}
	return choice, nil
}

// AskInput prompts for a free-text value with an optional default
func AskInput(message, defaultValue string) (string, error) {
	var value string
	prompt := &survey.Input{
		Message: message,
		Default: defaultValue,
	}
	if err := survey.AskOne(prompt, &value); err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

// AskConfirm asks a yes/no question
func AskConfirm(message string, defaultValue bool) (bool, error) {
	ok := defaultValue
	prompt := &survey.Confirm{
		Message: message,
		Default: defaultValue,
	}
	if err := survey.AskOne(prompt, &ok); err != nil {
		return false, err
	}
	return ok, nil
}
