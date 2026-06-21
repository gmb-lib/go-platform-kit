// Package config provides the shared base configuration that every backend
// service embeds. It builds on Azugo's configuration (azugo.io/azugo/config,
// which itself embeds azugo.io/core/config) and contributes the standard
// environment the whole fleet shares — service identity, the OpenTelemetry
// knobs, and broker access — so telemetry, correlation, and broker wiring look
// identical across all services.
//
// A service defines its own Configuration that embeds *BaseConfiguration and
// adds its own sub-config (DB DSN, audiences, …); go-platform-kit only owns the
// shared base.
//
//	type Configuration struct {
//	 *config.BaseConfiguration `mapstructure:",squash"`
//	 PostgresDSN string `mapstructure:"postgres_dsn" validate:"required"`
//	}
//
//	func NewConfiguration() *Configuration {
//	 return &Configuration{BaseConfiguration: config.New()}
//	}
package config

import (
	azugocfg "azugo.io/azugo/config"
	corecfg "azugo.io/core/config"
	"azugo.io/core/validation"
	"azugo.io/opentelemetry"
	"github.com/spf13/viper"
)

// BaseConfiguration is the shared configuration base. It embeds Azugo's
// application configuration (so it already satisfies azugocfg.Configurable via
// the promoted ServerCore method and core's Configurable via Core/Loaded) and
// adds the standard env every service needs.
type BaseConfiguration struct {
	*azugocfg.Configuration `mapstructure:",squash"`

	// ServiceName is the logical service id (env SERVICE_NAME). It is the broker
	// client id and the default label for the project metric helpers.
	//
	// The deployment environment is NOT re-declared here: Azugo already owns the
	// reserved ENVIRONMENT variable (development|test|staging|production) and
	// drives both the service.environment log field and the OpenTelemetry
	// deployment.environment resource attribute from app.Env(). Re-binding it with
	// a different vocabulary only made the two disagree, so the kit defers to
	// Azugo — read app.Env() when a service needs the environment.
	ServiceName string `mapstructure:"service_name" validate:"required"`

	// Telemetry is the OpenTelemetry configuration section (the standard OTEL_*
	// env, per azugo.io/opentelemetry). Wired once in platform.Setup; tracing
	// auto-disables when no endpoint is configured.
	Telemetry *opentelemetry.Configuration `mapstructure:"telemetry"`

	// Broker is the message-broker connection + TLS material (env BROKER_*).
	Broker *Broker `mapstructure:"broker"`
}

// New returns a BaseConfiguration with the embedded Azugo configuration
// initialized. Services build their own configuration on top of it:
//
//	&Configuration{BaseConfiguration: config.New()}
func New() *BaseConfiguration {
	return &BaseConfiguration{
		Configuration: azugocfg.New(),
	}
}

// Bind registers defaults and environment-variable bindings with viper. A
// service that embeds *BaseConfiguration MUST call this from its own Bind:
//
//	func (c *Configuration) Bind(_ string, v *viper.Viper) {
//	 c.BaseConfiguration.Bind("", v)
//	 // service-specific bindings…
//	}
func (c *BaseConfiguration) Bind(_ string, v *viper.Viper) {
	// Always bind the Azugo base (server, cors, metrics, healthz, http_client…).
	c.Configuration.Bind("", v)

	// Sub-sections.
	c.Telemetry = corecfg.Bind(c.Telemetry, "telemetry", v)
	c.Broker = corecfg.Bind(c.Broker, "broker", v)

	// ENVIRONMENT is bound by Azugo itself (app.Env()); the kit does not shadow it.
	_ = v.BindEnv("service_name", "SERVICE_NAME")
}

// Validate validates the full base configuration. The validator recurses into
// the embedded Azugo configuration and the Telemetry/Broker sub-sections, so a
// single Struct call covers every standard field and fails fast at startup on a
// missing/invalid value.
func (c *BaseConfiguration) Validate(valid *validation.Validate) error {
	return valid.Struct(c)
}

// Broker carries the message-broker connection details and TLS material. The
// endpoint is non-secret (ConfigMap→env); the TLS material is secret and is
// resolved from the secret store via the Vault-agent `<NAME>_FILE` convention.
type Broker struct {
	// URL is the broker endpoint (env BROKER_URL), e.g. "tls://broker:9093".
	URL string `mapstructure:"url"`
	// TLSCert / TLSKey / TLSCA hold the client TLS material (PEM), resolved from
	// the secret store (env BROKER_TLS_CERT_FILE / _KEY_FILE / _CA_FILE).
	TLSCert string `mapstructure:"tls_cert"`
	TLSKey  string `mapstructure:"tls_key"`
	TLSCA   string `mapstructure:"tls_ca"`
}

// Bind registers the broker defaults, env bindings, and secret resolution.
func (c *Broker) Bind(prefix string, v *viper.Viper) {
	loadSecret(v, prefix+".tls_cert", "BROKER_TLS_CERT")
	loadSecret(v, prefix+".tls_key", "BROKER_TLS_KEY")
	loadSecret(v, prefix+".tls_ca", "BROKER_TLS_CA")

	_ = v.BindEnv(prefix+".url", "BROKER_URL")
	_ = v.BindEnv(prefix+".tls_cert", "BROKER_TLS_CERT")
	_ = v.BindEnv(prefix+".tls_key", "BROKER_TLS_KEY")
	_ = v.BindEnv(prefix+".tls_ca", "BROKER_TLS_CA")
}

// Validate validates the broker configuration section.
func (c *Broker) Validate(valid *validation.Validate) error {
	return valid.Struct(c)
}

// loadSecret resolves a secret from the secret store (Vault agent → <NAME>_FILE)
// and registers it as a default so an explicit env value still overrides it.
func loadSecret(v *viper.Viper, key, name string) {
	if secret, err := corecfg.LoadRemoteSecret(name); err == nil && secret != "" {
		v.SetDefault(key, secret)
	}
}
