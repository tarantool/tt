package download

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
)

// CertificateError is a server whose TLS certificate tt cannot verify: one
// of an authority the system does not trust, or any at all on a system
// without its CA certificates, one of another host, one expired. The sources
// of the Community products report it rather than leave the version to the
// Git tags: git reaches its servers with the CA certificates of the same
// system.
type CertificateError struct {
	// Host is the server, or empty when the error does not name it.
	Host string
	// err is the failure as it was returned, cause the verification failure
	// in it.
	err   error
	cause error
}

// Error implements error. It names the server and the verification failure
// and, for an authority the system does not trust, how to make it trust it.
func (e *CertificateError) Error() string {
	server := e.Host
	if server == "" {
		server = "the server"
	}

	message := fmt.Sprintf("the TLS certificate of %s cannot be verified: %v", server, e.cause)

	_, unknownAuthority := errors.AsType[x509.UnknownAuthorityError](e.cause)
	_, noRoots := errors.AsType[x509.SystemRootsError](e.cause)

	if unknownAuthority || noRoots {
		message += "; install the CA certificates of the system (ca-certificates), " +
			"or set SSL_CERT_FILE or SSL_CERT_DIR to a CA bundle"
	}

	return message
}

// Unwrap returns the failure as it was returned.
func (e *CertificateError) Unwrap() error {
	return e.err
}

// AsCertificateError returns err as a *CertificateError if it is a failure
// to verify the TLS certificate of a server, with the server its request
// names.
func AsCertificateError(err error) (*CertificateError, bool) {
	certificate, ok := errors.AsType[*CertificateError](err)
	if ok {
		return certificate, true
	}

	cause := verificationFailure(err)
	if cause == nil {
		return nil, false
	}

	host := ""

	request, ok := errors.AsType[*url.Error](err)
	if ok {
		address, parseErr := url.Parse(request.URL)
		if parseErr == nil {
			host = address.Hostname()
		}
	}

	return &CertificateError{Host: host, err: err, cause: cause}, true
}

// verificationFailure returns the failure to verify a certificate err
// holds, or nil.
func verificationFailure(err error) error {
	verification, ok := errors.AsType[*tls.CertificateVerificationError](err)
	if ok {
		if verification.Err != nil {
			return verification.Err
		}

		return verification
	}

	if authority, ok := errors.AsType[x509.UnknownAuthorityError](err); ok {
		return authority
	}

	if invalid, ok := errors.AsType[x509.CertificateInvalidError](err); ok {
		return invalid
	}

	if hostname, ok := errors.AsType[x509.HostnameError](err); ok {
		return hostname
	}

	if roots, ok := errors.AsType[x509.SystemRootsError](err); ok {
		return roots
	}

	return nil
}
