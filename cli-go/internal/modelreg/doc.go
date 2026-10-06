// Package modelreg is the provider-aware model registry (K-138, plan phase P1).
//
// Before it, the model vocabulary was Claude-tier-only: haiku, sonnet, opus and
// fable. A model on another harness was an opaque string that a table in
// internal/runtime happened to know. The registry says what each model is: which
// harness runs it, who serves it, how it is billed, which reasoning efforts it
// takes, its limits, and (only for models billed per API call) its price.
//
// # Sources, in order of authority
//
//  1. The catalog, lib/settings/model-catalog.json, embedded in the binary. The
//     framework ships it; nothing a project or a user writes can change what the
//     binary believes about the catalog itself.
//  2. The user overlay, ~/.yakos-state/model-registry.yml. It may enable or
//     disable a model, state how a model is billed, override a price, map a tier
//     alias on a harness, and let discovered ids join the catalog. It loosens, so
//     it is read only through the owner-only trust check (statepath.ReadTrusted)
//     and only from statepath.TrustedDir(), never from a path a project's
//     environment could move (K-129).
//  3. The project, <project>/.yakos.yml `models:`. It may only DISABLE models. A
//     project cannot enable, add or alias anything, so a cloned repository can
//     narrow what runs on its behalf and never widen it (the rule decision.Tighten
//     applies to the decision provider).
//  4. Discovery, `agy models`. It adds an "available" flag to catalog entries and
//     never adds a catalog entry unless the overlay admits the harness.
//
// # What this package does not do
//
// It does not choose a model (that is the router, K-139), does not call a vendor
// API, does not read a credential, and does not change what dispatch sends today:
// the alias table in internal/runtime still resolves a `model:` alias, and
// lib/settings/model-aliases.json (the file the bash CLI reads) stays byte for byte
// what it was. The catalog's own `aliases` key is kept equal to it by a drift test.
//
// # Import rules
//
// The package imports only the standard library, yaml.v3 and internal/statepath.
// It deliberately does not import internal/auth or internal/runtime: auth imports
// runtime, and runtime is expected to read the registry later, so either import
// would close a cycle. What those packages know (is a harness signed in, which
// environment may a harness see) reaches discovery through injected functions.
package modelreg
