/*
Package openstack contains resources for the individual OpenStack projects
supported in Gophercloud. It also includes functions to authenticate to an
OpenStack cloud and for provisioning various service-level clients.

Example of Creating a Service Client

	ao, err := openstack.AuthOptionsFromEnv()
	provider, err := openstack.AuthenticatedClient(context.TODO(), ao)
	client, err := openstack.NewNetworkV2(context.TODO(), provider, gophercloud.EndpointOpts{
		Region: os.Getenv("OS_REGION_NAME"),
	})
*/
package openstack
