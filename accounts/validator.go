package accounts

import "github.com/concourse/flag"

var DefaultValidator = func(cmd Command) error {
	err := validateFileFlag(cmd.Postgres.CACert.Path())
	if err != nil {
		return err
	}
	err = validateFileFlag(cmd.Postgres.ClientCert.Path())
	if err != nil {
		return err
	}
	err = validateFileFlag(cmd.Postgres.ClientKey.Path())
	if err != nil {
		return err
	}
	return nil
}

func validateFileFlag(path string) error {
	if path == "" {
		return nil
	}
	var file flag.File
	return file.UnmarshalFlag(path)
}
