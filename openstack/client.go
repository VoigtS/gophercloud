package openstack

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/gophercloud/gophercloud/v2"
	tokens2 "github.com/gophercloud/gophercloud/v2/openstack/identity/v2/tokens"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/ec2tokens"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/oauth1"
	tokens3 "github.com/gophercloud/gophercloud/v2/openstack/identity/v3/tokens"
	"github.com/gophercloud/gophercloud/v2/openstack/utils"
)

const (
	// v2 represents Keystone v2.
	// It should never increase beyond 2.0.
	v2 = "v2.0"

	// v3 represents Keystone v3.
	// The version can be anything from v3 to v3.x.
	v3 = "v3"
)

// NewClient prepares an unauthenticated ProviderClient instance.
// Most users will probably prefer using the AuthenticatedClient function
// instead.
//
// This is useful if you wish to explicitly control the version of the identity
// service that's used for authentication explicitly, for example.
//
// A basic example of using this would be:
//
//	ao, err := openstack.AuthOptionsFromEnv()
//	provider, err := openstack.NewClient(ao.IdentityEndpoint)
//	client, err := openstack.NewIdentityV3(ctx, provider, gophercloud.EndpointOpts{})
func NewClient(endpoint string) (*gophercloud.ProviderClient, error) {
	base, err := utils.BaseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}

	endpoint = gophercloud.NormalizeURL(endpoint)
	base = gophercloud.NormalizeURL(base)

	p := new(gophercloud.ProviderClient)
	p.IdentityBase = base
	p.IdentityEndpoint = endpoint
	p.UseTokenLock()

	return p, nil
}

// AuthenticatedClient logs in to an OpenStack cloud found at the identity endpoint
// specified by the options, acquires a token, and returns a Provider Client
// instance that's ready to operate.
//
// If the full path to a versioned identity endpoint was specified  (example:
// http://example.com:5000/v3), that path will be used as the endpoint to query.
//
// If a versionless endpoint was specified (example: http://example.com:5000/),
// the endpoint will be queried to determine which versions of the identity service
// are available, then chooses the most recent or most supported version.
//
// Example:
//
//	ao, err := openstack.AuthOptionsFromEnv()
//	provider, err := openstack.AuthenticatedClient(ctx, ao)
//	client, err := openstack.NewNetworkV2(ctx, provider, gophercloud.EndpointOpts{
//		Region: os.Getenv("OS_REGION_NAME"),
//	})
func AuthenticatedClient(ctx context.Context, options gophercloud.AuthOptions) (*gophercloud.ProviderClient, error) {
	client, err := NewClient(options.IdentityEndpoint)
	if err != nil {
		return nil, err
	}

	err = Authenticate(ctx, client, options)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// Authenticate authenticates or re-authenticates against the most
// recent identity service supported at the provided endpoint.
func Authenticate(ctx context.Context, client *gophercloud.ProviderClient, options gophercloud.AuthOptions) error {
	versions := []*utils.Version{
		{ID: v2, Priority: 20, Suffix: "/v2.0/"},
		{ID: v3, Priority: 30, Suffix: "/v3/"},
	}

	chosen, endpoint, err := utils.ChooseVersion(ctx, client, versions)
	if err != nil {
		return err
	}

	switch chosen.ID {
	case v2:
		return v2auth(ctx, client, endpoint, &options, gophercloud.EndpointOpts{})
	case v3:
		return v3auth(ctx, client, endpoint, &options, gophercloud.EndpointOpts{})
	default:
		// The switch statement must be out of date from the versions list.
		return fmt.Errorf("unrecognized identity version: %s", chosen.ID)
	}
}

// AuthenticateV2 explicitly authenticates against the identity v2 endpoint.
func AuthenticateV2(ctx context.Context, client *gophercloud.ProviderClient, options tokens2.AuthOptionsBuilder, eo gophercloud.EndpointOpts) error {
	return v2auth(ctx, client, "", options, eo)
}

type v2TokenNoReauth struct {
	tokens2.AuthOptionsBuilder
}

func (v2TokenNoReauth) CanReauth() bool { return false }

func v2auth(ctx context.Context, client *gophercloud.ProviderClient, endpoint string, options tokens2.AuthOptionsBuilder, eo gophercloud.EndpointOpts) error {
	v2Client, err := NewIdentityV2(ctx, client, eo)
	if err != nil {
		return err
	}

	if endpoint != "" {
		v2Client.Endpoint = endpoint
	}

	result := tokens2.Create(ctx, v2Client, options)

	err = client.SetTokenAndAuthResult(result)
	if err != nil {
		return err
	}

	catalog, err := result.ExtractServiceCatalog()
	if err != nil {
		return err
	}

	if options.CanReauth() {
		// here we're creating a throw-away client (tac). it's a copy of the user's provider client, but
		// with the token and reauth func zeroed out. combined with setting `AllowReauth` to `false`,
		// this should retry authentication only once
		tac := *client
		tac.SetThrowaway(true)
		tac.ReauthFunc = nil
		err := tac.SetTokenAndAuthResult(nil)
		if err != nil {
			return err
		}
		client.ReauthFunc = func(ctx context.Context) error {
			err := v2auth(ctx, &tac, endpoint, &v2TokenNoReauth{options}, eo)
			if err != nil {
				return err
			}
			client.CopyTokenFrom(&tac)
			return nil
		}
	}
	client.EndpointLocator = func(ctx context.Context, opts gophercloud.EndpointOpts) (string, error) {
		return V2Endpoint(ctx, client, catalog, opts)
	}

	return nil
}

// AuthenticateV3 explicitly authenticates against the identity v3 service.
func AuthenticateV3(ctx context.Context, client *gophercloud.ProviderClient, options tokens3.AuthOptionsBuilder, eo gophercloud.EndpointOpts) error {
	return v3auth(ctx, client, "", options, eo)
}

func v3auth(ctx context.Context, client *gophercloud.ProviderClient, endpoint string, opts tokens3.AuthOptionsBuilder, eo gophercloud.EndpointOpts) error {
	// Override the generated service endpoint with the one returned by the version endpoint.
	v3Client, err := NewIdentityV3(ctx, client, eo)
	if err != nil {
		return err
	}

	if endpoint != "" {
		v3Client.Endpoint = endpoint
	}

	var catalog *tokens3.ServiceCatalog

	var tokenID string
	// passthroughToken allows to passthrough the token without a scope
	var passthroughToken bool
	switch v := opts.(type) {
	case *gophercloud.AuthOptions:
		tokenID = v.TokenID
		passthroughToken = (v.Scope == nil || *v.Scope == gophercloud.AuthScope{})
	case *tokens3.AuthOptions:
		tokenID = v.TokenID
		passthroughToken = (v.Scope == tokens3.Scope{})
	}

	if tokenID != "" && passthroughToken {
		// passing through the token ID without requesting a new scope
		if opts.CanReauth() {
			return fmt.Errorf("cannot use AllowReauth, when the token ID is defined and auth scope is not set")
		}

		v3Client.SetToken(tokenID)
		result := tokens3.Get(ctx, v3Client, tokenID)
		if result.Err != nil {
			return result.Err
		}

		err = client.SetTokenAndAuthResult(result)
		if err != nil {
			return err
		}

		catalog, err = result.ExtractServiceCatalog()
		if err != nil {
			return err
		}
	} else {
		var result tokens3.CreateResult
		switch opts.(type) {
		case *ec2tokens.AuthOptions:
			result = ec2tokens.Create(ctx, v3Client, opts)
		case *oauth1.AuthOptions:
			result = oauth1.Create(ctx, v3Client, opts)
		default:
			result = tokens3.Create(ctx, v3Client, opts)
		}

		err = client.SetTokenAndAuthResult(result)
		if err != nil {
			return err
		}

		catalog, err = result.ExtractServiceCatalog()
		if err != nil {
			return err
		}
	}

	if opts.CanReauth() {
		// here we're creating a throw-away client (tac). it's a copy of the user's provider client, but
		// with the token and reauth func zeroed out. combined with setting `AllowReauth` to `false`,
		// this should retry authentication only once
		tac := *client
		tac.SetThrowaway(true)
		tac.ReauthFunc = nil
		err = tac.SetTokenAndAuthResult(nil)
		if err != nil {
			return err
		}
		var tao tokens3.AuthOptionsBuilder
		switch ot := opts.(type) {
		case *gophercloud.AuthOptions:
			o := *ot
			o.AllowReauth = false
			tao = &o
		case *tokens3.AuthOptions:
			o := *ot
			o.AllowReauth = false
			tao = &o
		case *ec2tokens.AuthOptions:
			o := *ot
			o.AllowReauth = false
			tao = &o
		case *oauth1.AuthOptions:
			o := *ot
			o.AllowReauth = false
			tao = &o
		default:
			tao = opts
		}
		client.ReauthFunc = func(ctx context.Context) error {
			err := v3auth(ctx, &tac, endpoint, tao, eo)
			if err != nil {
				return err
			}
			client.CopyTokenFrom(&tac)
			return nil
		}
	}
	client.EndpointLocator = func(ctx context.Context, opts gophercloud.EndpointOpts) (string, error) {
		return V3Endpoint(ctx, client, catalog, opts)
	}

	return nil
}

// NewIdentityV2 creates a ServiceClient that may be used to interact with the
// v2 identity service.
func NewIdentityV2(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	endpoint := client.IdentityBase + "v2.0/"
	clientType := "identity"
	var err error
	if !reflect.DeepEqual(eo, gophercloud.EndpointOpts{}) {
		eo.ApplyDefaults(clientType)
		endpoint, err = client.EndpointLocator(ctx, eo)
		if err != nil {
			return nil, err
		}
	}

	return &gophercloud.ServiceClient{
		ProviderClient: client,
		Endpoint:       endpoint,
		Type:           clientType,
	}, nil
}

// NewIdentityV3 creates a ServiceClient that may be used to access the v3
// identity service.
func NewIdentityV3(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	endpoint := client.IdentityBase + "v3/"
	clientType := "identity"
	var err error
	if !reflect.DeepEqual(eo, gophercloud.EndpointOpts{}) {
		eo.ApplyDefaults(clientType)
		endpoint, err = client.EndpointLocator(ctx, eo)
		if err != nil {
			return nil, err
		}
	}

	// Ensure endpoint still has a suffix of v3.
	// This is because EndpointLocator might have found a versionless
	// endpoint or the published endpoint is still /v2.0. In both
	// cases, we need to fix the endpoint to point to /v3.
	base, err := utils.BaseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}

	base = gophercloud.NormalizeURL(base)

	endpoint = base + "v3/"

	return &gophercloud.ServiceClient{
		ProviderClient: client,
		Endpoint:       endpoint,
		Type:           clientType,
	}, nil
}

// TODO(stephenfin): Allow passing aliases to all New${SERVICE}V${VERSION} methods in v3
func initClientOpts(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts, clientType string, version int) (*gophercloud.ServiceClient, error) {
	sc := new(gophercloud.ServiceClient)

	eo.ApplyDefaults(clientType)
	if eo.Version != 0 && eo.Version != version {
		return sc, errors.New("conflict between requested service major version and manually set version")
	}
	eo.Version = version

	url, err := client.EndpointLocator(ctx, eo)
	if err != nil {
		return sc, err
	}

	sc.ProviderClient = client
	sc.Endpoint = url
	sc.Type = clientType
	return sc, nil
}

// NewBareMetalV1 creates a ServiceClient that may be used with the v1
// bare metal package.
func NewBareMetalV1(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	sc, err := initClientOpts(ctx, client, eo, "baremetal", 1)
	if !strings.HasSuffix(strings.TrimSuffix(sc.Endpoint, "/"), "v1") {
		sc.ResourceBase = sc.Endpoint + "v1/"
	}
	return sc, err
}

// NewBareMetalIntrospectionV1 creates a ServiceClient that may be used with the v1
// bare metal introspection package.
func NewBareMetalIntrospectionV1(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	return initClientOpts(ctx, client, eo, "baremetal-introspection", 1)
}

// NewObjectStorageV1 creates a ServiceClient that may be used with the v1
// object storage package.
func NewObjectStorageV1(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	return initClientOpts(ctx, client, eo, "object-store", 1)
}

// NewComputeV2 creates a ServiceClient that may be used with the v2 compute
// package.
func NewComputeV2(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	return initClientOpts(ctx, client, eo, "compute", 2)
}

// NewNetworkV2 creates a ServiceClient that may be used with the v2 network
// package.
func NewNetworkV2(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	sc, err := initClientOpts(ctx, client, eo, "network", 2)
	sc.ResourceBase = sc.Endpoint + "v2.0/"
	return sc, err
}

// TODO(stephenfin): Remove this in v3. We no longer support the V1 Block Storage service.
// NewBlockStorageV1 creates a ServiceClient that may be used to access the v1
// block storage service.
func NewBlockStorageV1(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	return initClientOpts(ctx, client, eo, "volume", 1)
}

// NewBlockStorageV2 creates a ServiceClient that may be used to access the v2
// block storage service.
func NewBlockStorageV2(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	return initClientOpts(ctx, client, eo, "block-storage", 2)
}

// NewBlockStorageV3 creates a ServiceClient that may be used to access the v3 block storage service.
func NewBlockStorageV3(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	return initClientOpts(ctx, client, eo, "block-storage", 3)
}

// NewSharedFileSystemV2 creates a ServiceClient that may be used to access the v2 shared file system service.
func NewSharedFileSystemV2(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	return initClientOpts(ctx, client, eo, "shared-file-system", 2)
}

// NewOrchestrationV1 creates a ServiceClient that may be used to access the v1
// orchestration service.
func NewOrchestrationV1(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	return initClientOpts(ctx, client, eo, "orchestration", 1)
}

// NewDBV1 creates a ServiceClient that may be used to access the v1 DB service.
func NewDBV1(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	return initClientOpts(ctx, client, eo, "database", 1)
}

// NewDNSV2 creates a ServiceClient that may be used to access the v2 DNS
// service.
func NewDNSV2(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	sc, err := initClientOpts(ctx, client, eo, "dns", 2)
	sc.ResourceBase = sc.Endpoint + "v2/"
	return sc, err
}

// NewImageV2 creates a ServiceClient that may be used to access the v2 image
// service.
func NewImageV2(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	sc, err := initClientOpts(ctx, client, eo, "image", 2)
	sc.ResourceBase = sc.Endpoint + "v2/"
	return sc, err
}

// NewLoadBalancerV2 creates a ServiceClient that may be used to access the v2
// load balancer service.
func NewLoadBalancerV2(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	sc, err := initClientOpts(ctx, client, eo, "load-balancer", 2)

	// Fixes edge case having an OpenStack lb endpoint with trailing version number.
	endpoint := strings.ReplaceAll(sc.Endpoint, "v2.0/", "")

	sc.ResourceBase = endpoint + "v2.0/"
	return sc, err
}

// NewMessagingV2 creates a ServiceClient that may be used with the v2 messaging
// service.
func NewMessagingV2(ctx context.Context, client *gophercloud.ProviderClient, clientID string, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	sc, err := initClientOpts(ctx, client, eo, "message", 2)
	sc.MoreHeaders = map[string]string{"Client-ID": clientID}
	return sc, err
}

// NewContainerV1 creates a ServiceClient that may be used with v1 container package
func NewContainerV1(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	return initClientOpts(ctx, client, eo, "application-container", 1)
}

// NewKeyManagerV1 creates a ServiceClient that may be used with the v1 key
// manager service.
func NewKeyManagerV1(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	sc, err := initClientOpts(ctx, client, eo, "key-manager", 1)
	sc.ResourceBase = sc.Endpoint + "v1/"
	return sc, err
}

// NewContainerInfraV1 creates a ServiceClient that may be used with the v1 container infra management
// package.
func NewContainerInfraV1(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	return initClientOpts(ctx, client, eo, "container-infrastructure-management", 1)
}

// NewWorkflowV2 creates a ServiceClient that may be used with the v2 workflow management package.
func NewWorkflowV2(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	return initClientOpts(ctx, client, eo, "workflow", 2)
}

// NewPlacementV1 creates a ServiceClient that may be used with the placement package.
func NewPlacementV1(ctx context.Context, client *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error) {
	return initClientOpts(ctx, client, eo, "placement", 1)
}
