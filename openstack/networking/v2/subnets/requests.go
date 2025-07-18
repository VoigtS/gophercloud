package subnets

import (
	"context"
	"fmt"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/pagination"
)

// ListOptsBuilder allows extensions to add additional parameters to the
// List request.
type ListOptsBuilder interface {
	ToSubnetListQuery() (string, error)
}

// ListOpts allows the filtering and sorting of paginated collections through
// the API. Filtering is achieved by passing in struct field values that map to
// the subnet attributes you want to see returned. SortKey allows you to sort
// by a particular subnet attribute. SortDir sets the direction, and is either
// `asc' or `desc'. Marker and Limit are used for pagination.
type ListOpts struct {
	Name              string `q:"name"`
	Description       string `q:"description"`
	DNSPublishFixedIP *bool  `q:"dns_publish_fixed_ip"`
	EnableDHCP        *bool  `q:"enable_dhcp"`
	NetworkID         string `q:"network_id"`
	TenantID          string `q:"tenant_id"`
	ProjectID         string `q:"project_id"`
	IPVersion         int    `q:"ip_version"`
	GatewayIP         string `q:"gateway_ip"`
	CIDR              string `q:"cidr"`
	IPv6AddressMode   string `q:"ipv6_address_mode"`
	IPv6RAMode        string `q:"ipv6_ra_mode"`
	ID                string `q:"id"`
	SubnetPoolID      string `q:"subnetpool_id"`
	Limit             int    `q:"limit"`
	Marker            string `q:"marker"`
	SortKey           string `q:"sort_key"`
	SortDir           string `q:"sort_dir"`
	Tags              string `q:"tags"`
	TagsAny           string `q:"tags-any"`
	NotTags           string `q:"not-tags"`
	NotTagsAny        string `q:"not-tags-any"`
	RevisionNumber    *int   `q:"revision_number"`
	SegmentID         string `q:"segment_id"`
}

// ToSubnetListQuery formats a ListOpts into a query string.
func (opts ListOpts) ToSubnetListQuery() (string, error) {
	q, err := gophercloud.BuildQueryString(opts)
	return q.String(), err
}

// List returns a Pager which allows you to iterate over a collection of
// subnets. It accepts a ListOpts struct, which allows you to filter and sort
// the returned collection for greater efficiency.
//
// Default policy settings return only those subnets that are owned by the tenant
// who submits the request, unless the request is submitted by a user with
// administrative rights.
func List(c *gophercloud.ServiceClient, opts ListOptsBuilder) pagination.Pager {
	url := listURL(c)
	if opts != nil {
		query, err := opts.ToSubnetListQuery()
		if err != nil {
			return pagination.Pager{Err: err}
		}
		url += query
	}
	return pagination.NewPager(c, url, func(r pagination.PageResult) pagination.Page {
		return SubnetPage{pagination.LinkedPageBase{PageResult: r}}
	})
}

// Get retrieves a specific subnet based on its unique ID.
func Get(ctx context.Context, c *gophercloud.ServiceClient, id string) (r GetResult) {
	resp, err := c.Get(ctx, getURL(c, id), &r.Body, nil)
	_, r.Header, r.Err = gophercloud.ParseResponse(resp, err)
	return
}

// CreateOptsBuilder allows extensions to add additional parameters to the
// List request.
type CreateOptsBuilder interface {
	ToSubnetCreateMap() (map[string]any, error)
}

// CreateOpts represents the attributes used when creating a new subnet.
type CreateOpts struct {
	// NetworkID is the UUID of the network the subnet will be associated with.
	NetworkID string `json:"network_id" required:"true"`

	// CIDR is the address CIDR of the subnet.
	CIDR string `json:"cidr,omitempty"`

	// Name is a human-readable name of the subnet.
	Name string `json:"name,omitempty"`

	// Description of the subnet.
	Description string `json:"description,omitempty"`

	// The UUID of the project who owns the Subnet. Only administrative users
	// can specify a project UUID other than their own.
	TenantID string `json:"tenant_id,omitempty"`

	// The UUID of the project who owns the Subnet. Only administrative users
	// can specify a project UUID other than their own.
	ProjectID string `json:"project_id,omitempty"`

	// AllocationPools are IP Address pools that will be available for DHCP.
	AllocationPools []AllocationPool `json:"allocation_pools,omitempty"`

	// GatewayIP sets gateway information for the subnet. Setting to nil will
	// cause a default gateway to automatically be created. Setting to an empty
	// string will cause the subnet to be created with no gateway. Setting to
	// an explicit address will set that address as the gateway.
	GatewayIP *string `json:"gateway_ip,omitempty"`

	// IPVersion is the IP version for the subnet.
	IPVersion gophercloud.IPVersion `json:"ip_version,omitempty"`

	// EnableDHCP will either enable to disable the DHCP service.
	EnableDHCP *bool `json:"enable_dhcp,omitempty"`

	// DNSNameservers are the nameservers to be set via DHCP.
	DNSNameservers []string `json:"dns_nameservers,omitempty"`

	// DNSPublishFixedIP will either enable or disable the publication of fixed IPs to the DNS
	DNSPublishFixedIP *bool `json:"dns_publish_fixed_ip,omitempty"`

	// ServiceTypes are the service types associated with the subnet.
	ServiceTypes []string `json:"service_types,omitempty"`

	// HostRoutes are any static host routes to be set via DHCP.
	HostRoutes []HostRoute `json:"host_routes,omitempty"`

	// The IPv6 address modes specifies mechanisms for assigning IPv6 IP addresses.
	IPv6AddressMode string `json:"ipv6_address_mode,omitempty"`

	// The IPv6 router advertisement specifies whether the networking service
	// should transmit ICMPv6 packets.
	IPv6RAMode string `json:"ipv6_ra_mode,omitempty"`

	// SubnetPoolID is the id of the subnet pool that subnet should be associated to.
	SubnetPoolID string `json:"subnetpool_id,omitempty"`

	// Prefixlen is used when user creates a subnet from the subnetpool. It will
	// overwrite the "default_prefixlen" value of the referenced subnetpool.
	Prefixlen int `json:"prefixlen,omitempty"`

	// SegmentID is a network segment the subnet is associated with. It is
	// available when segment extension is enabled.
	SegmentID string `json:"segment_id,omitempty"`
}

// ToSubnetCreateMap builds a request body from CreateOpts.
func (opts CreateOpts) ToSubnetCreateMap() (map[string]any, error) {
	b, err := gophercloud.BuildRequestBody(opts, "subnet")
	if err != nil {
		return nil, err
	}

	if m := b["subnet"].(map[string]any); m["gateway_ip"] == "" {
		m["gateway_ip"] = nil
	}

	return b, nil
}

// Create accepts a CreateOpts struct and creates a new subnet using the values
// provided. You must remember to provide a valid NetworkID, CIDR and IP
// version.
func Create(ctx context.Context, c *gophercloud.ServiceClient, opts CreateOptsBuilder) (r CreateResult) {
	b, err := opts.ToSubnetCreateMap()
	if err != nil {
		r.Err = err
		return
	}
	resp, err := c.Post(ctx, createURL(c), b, &r.Body, nil)
	_, r.Header, r.Err = gophercloud.ParseResponse(resp, err)
	return
}

// UpdateOptsBuilder allows extensions to add additional parameters to the
// Update request.
type UpdateOptsBuilder interface {
	ToSubnetUpdateMap() (map[string]any, error)
}

// UpdateOpts represents the attributes used when updating an existing subnet.
type UpdateOpts struct {
	// Name is a human-readable name of the subnet.
	Name *string `json:"name,omitempty"`

	// Description of the subnet.
	Description *string `json:"description,omitempty"`

	// AllocationPools are IP Address pools that will be available for DHCP.
	AllocationPools []AllocationPool `json:"allocation_pools,omitempty"`

	// GatewayIP sets gateway information for the subnet. Setting to an empty
	// string will cause the subnet to not have a gateway. Setting to
	// an explicit address will set that address as the gateway.
	GatewayIP *string `json:"gateway_ip,omitempty"`

	// DNSNameservers are the nameservers to be set via DHCP.
	DNSNameservers *[]string `json:"dns_nameservers,omitempty"`

	// DNSPublishFixedIP will either enable or disable the publication of fixed IPs to the DNS
	DNSPublishFixedIP *bool `json:"dns_publish_fixed_ip,omitempty"`

	// ServiceTypes are the service types associated with the subnet.
	ServiceTypes *[]string `json:"service_types,omitempty"`

	// HostRoutes are any static host routes to be set via DHCP.
	HostRoutes *[]HostRoute `json:"host_routes,omitempty"`

	// EnableDHCP will either enable to disable the DHCP service.
	EnableDHCP *bool `json:"enable_dhcp,omitempty"`

	// RevisionNumber implements extension:standard-attr-revisions. If != "" it
	// will set revision_number=%s. If the revision number does not match, the
	// update will fail.
	RevisionNumber *int `json:"-" h:"If-Match"`

	// SegmentID is a network segment the subnet is associated with. It is
	// available when segment extension is enabled.
	SegmentID *string `json:"segment_id,omitempty"`
}

// ToSubnetUpdateMap builds a request body from UpdateOpts.
func (opts UpdateOpts) ToSubnetUpdateMap() (map[string]any, error) {
	b, err := gophercloud.BuildRequestBody(opts, "subnet")
	if err != nil {
		return nil, err
	}

	if m := b["subnet"].(map[string]any); m["gateway_ip"] == "" {
		m["gateway_ip"] = nil
	}

	return b, nil
}

// Update accepts a UpdateOpts struct and updates an existing subnet using the
// values provided.
func Update(ctx context.Context, c *gophercloud.ServiceClient, id string, opts UpdateOptsBuilder) (r UpdateResult) {
	b, err := opts.ToSubnetUpdateMap()
	if err != nil {
		r.Err = err
		return
	}
	h, err := gophercloud.BuildHeaders(opts)
	if err != nil {
		r.Err = err
		return
	}
	for k := range h {
		if k == "If-Match" {
			h[k] = fmt.Sprintf("revision_number=%s", h[k])
		}
	}

	resp, err := c.Put(ctx, updateURL(c, id), b, &r.Body, &gophercloud.RequestOpts{
		MoreHeaders: h,
		OkCodes:     []int{200, 201},
	})
	_, r.Header, r.Err = gophercloud.ParseResponse(resp, err)
	return
}

// Delete accepts a unique ID and deletes the subnet associated with it.
func Delete(ctx context.Context, c *gophercloud.ServiceClient, id string) (r DeleteResult) {
	resp, err := c.Delete(ctx, deleteURL(c, id), nil)
	_, r.Header, r.Err = gophercloud.ParseResponse(resp, err)
	return
}
