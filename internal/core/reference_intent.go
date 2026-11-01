package core

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
)

// ReferenceRoleSchema describes vendor-independent media roles in an ordered
// reference array. Only an adapter translates these reviewed roles to wire keys.
type ReferenceRoleSchema struct {
	Kind    string   `json:"kind"`
	Roles   []string `json:"roles"`
	Default string   `json:"default"`
}

var referenceAddress = regexp.MustCompile(`^(image|video|audio)_([1-9][0-9]*)$`)

func ReferenceRole(model MediaModel, options MediaOptions, kind string, index int) string {
	key := kind + "_" + strconv.Itoa(index+1)
	if value := options.ReferenceRoles[key]; value != "" {
		return value
	}
	for _, schema := range model.ReferenceRoleSchemas {
		if schema.Kind == kind {
			return schema.Default
		}
	}
	return "reference"
}

func ValidateReferenceRoles(model MediaModel, options MediaOptions) error {
	if len(options.ReferenceRoles) > 64 {
		return fmt.Errorf("%w: too many reference roles", ErrOptionsInvalid)
	}
	for address, role := range options.ReferenceRoles {
		parts := referenceAddress.FindStringSubmatch(address)
		if parts == nil {
			return fmt.Errorf("%w: invalid reference role address", ErrOptionsInvalid)
		}
		index, err := strconv.Atoi(parts[2])
		if err != nil {
			return ErrOptionsInvalid
		}
		count := len(options.ImageURLs)
		if parts[1] == "video" {
			count = len(options.Videos())
		}
		if parts[1] == "audio" {
			count = len(options.Audios())
		}
		if index > count {
			return fmt.Errorf("%w: role has no matching reference", ErrOptionsInvalid)
		}
		allowed := false
		for _, schema := range model.ReferenceRoleSchemas {
			if schema.Kind == parts[1] && slices.Contains(schema.Roles, role) {
				allowed = true
			}
		}
		if !allowed {
			return fmt.Errorf("%w: unsupported reference role", ErrOptionsInvalid)
		}
	}
	return nil
}
