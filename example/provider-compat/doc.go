// Package providercompat contains compatibility tests for third-party LLM
// providers that implement the Chat Completions protocol.
//
// These tests verify that the shared chatcompat provider works correctly with
// the wire format used by each vendor. They use httptest servers and do not
// make real API calls.
package providercompat
