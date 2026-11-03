package companyinterface

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

// CustomerAuthority is implemented only by central persistence. Projection is
// called with a freshly verified subject, never an email or browser assertion.
type CustomerAuthority interface {
	Authority
	CentralKeys
	RecipientPolicy
	User(context.Context, string) (identity.User, error)
}

const InstallationHeader = "X-Gateway-Company-Installation"
const SessionHeader = "X-Gateway-Company-Session"
const LocalSessionHeader = "X-Gateway-Company-Local-Session"
const LocalOriginHeader = "X-Gateway-Company-Origin"
const CustomerBrowserPrefix = "/auth/company-interface/v1/customer-browser/"
const CustomerSDKPrefix = "/auth/company-interface/v1/customer-sdk/"
