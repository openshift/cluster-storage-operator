// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

import (
	operatorv1 "github.com/openshift/api/operator/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// ConfigSpecApplyConfiguration represents an declarative configuration of the ConfigSpec type for use
// with apply.
type ConfigSpecApplyConfiguration struct {
	OperatorSpecApplyConfiguration `json:",inline"`
}

// ConfigSpecApplyConfiguration constructs an declarative configuration of the ConfigSpec type for use with
// apply.
func ConfigSpec() *ConfigSpecApplyConfiguration {
	return &ConfigSpecApplyConfiguration{}
}

// WithManagementState sets the ManagementState field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ManagementState field is set to the value of the last call.
func (b *ConfigSpecApplyConfiguration) WithManagementState(value operatorv1.ManagementState) *ConfigSpecApplyConfiguration {
	b.ManagementState = &value
	return b
}

// WithLogLevel sets the LogLevel field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the LogLevel field is set to the value of the last call.
func (b *ConfigSpecApplyConfiguration) WithLogLevel(value operatorv1.LogLevel) *ConfigSpecApplyConfiguration {
	b.LogLevel = &value
	return b
}

// WithOperatorLogLevel sets the OperatorLogLevel field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the OperatorLogLevel field is set to the value of the last call.
func (b *ConfigSpecApplyConfiguration) WithOperatorLogLevel(value operatorv1.LogLevel) *ConfigSpecApplyConfiguration {
	b.OperatorLogLevel = &value
	return b
}

// WithUnsupportedConfigOverrides sets the UnsupportedConfigOverrides field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the UnsupportedConfigOverrides field is set to the value of the last call.
func (b *ConfigSpecApplyConfiguration) WithUnsupportedConfigOverrides(value runtime.RawExtension) *ConfigSpecApplyConfiguration {
	b.UnsupportedConfigOverrides = &value
	return b
}

// WithObservedConfig sets the ObservedConfig field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ObservedConfig field is set to the value of the last call.
func (b *ConfigSpecApplyConfiguration) WithObservedConfig(value runtime.RawExtension) *ConfigSpecApplyConfiguration {
	b.ObservedConfig = &value
	return b
}