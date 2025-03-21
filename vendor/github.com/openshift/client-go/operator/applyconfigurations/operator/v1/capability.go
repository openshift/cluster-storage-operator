// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

import (
	operatorv1 "github.com/openshift/api/operator/v1"
)

// CapabilityApplyConfiguration represents a declarative configuration of the Capability type for use
// with apply.
type CapabilityApplyConfiguration struct {
	Name       *operatorv1.ConsoleCapabilityName       `json:"name,omitempty"`
	Visibility *CapabilityVisibilityApplyConfiguration `json:"visibility,omitempty"`
}

// CapabilityApplyConfiguration constructs a declarative configuration of the Capability type for use with
// apply.
func Capability() *CapabilityApplyConfiguration {
	return &CapabilityApplyConfiguration{}
}

// WithName sets the Name field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Name field is set to the value of the last call.
func (b *CapabilityApplyConfiguration) WithName(value operatorv1.ConsoleCapabilityName) *CapabilityApplyConfiguration {
	b.Name = &value
	return b
}

// WithVisibility sets the Visibility field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Visibility field is set to the value of the last call.
func (b *CapabilityApplyConfiguration) WithVisibility(value *CapabilityVisibilityApplyConfiguration) *CapabilityApplyConfiguration {
	b.Visibility = value
	return b
}
