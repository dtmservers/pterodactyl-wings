package environment

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"

	"github.com/pterodactyl/wings/config"
)

var _conce sync.Once
var _client *client.Client

// Docker returns a docker client to be used throughout the codebase. Once a
// client has been created it will be returned for all subsequent calls to this
// function.
func Docker() (*client.Client, error) {
	var err error
	_conce.Do(func() {
		_client, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	})
	return _client, errors.Wrap(err, "environment/docker: could not create client")
}

// ConfigureDocker configures the required network for the docker environment.
func ConfigureDocker(ctx context.Context) error {
	// Ensure the required docker network exists on the system.
	cli, err := Docker()
	if err != nil {
		return err
	}

	nw := config.Get().Docker.Network
	resource, err := cli.NetworkInspect(ctx, nw.Name, types.NetworkInspectOptions{})
	if err != nil {
		if client.IsErrNotFound(err) {
			log.Info("creating missing pterodactyl0 interface, this could take a few seconds...")
			if err := createDockerNetwork(ctx, cli); err != nil {
				return err
			}
		} else {
			return err
		}
	}

	config.Update(func(c *config.Configuration) {
		c.Docker.Network.Driver = resource.Driver
		switch c.Docker.Network.Driver {
		case "host":
			c.Docker.Network.Interface = "127.0.0.1"
			c.Docker.Network.ISPN = false
		case "overlay":
			fallthrough
		case "weavemesh":
			c.Docker.Network.Interface = ""
			c.Docker.Network.ISPN = true
		default:
			c.Docker.Network.ISPN = false
		}
	})
	return nil
}

// Creates a new network on the machine if one does not exist already.
func createDockerNetwork(ctx context.Context, cli *client.Client) error {
	nw := config.Get().Docker.Network

	var options = make(map[string]string)

	if nw.Driver == "bridge" {
		options["encryption"] = "false"
		options["com.docker.network.bridge.default_bridge"] = "false"
		options["com.docker.network.bridge.enable_icc"] = strconv.FormatBool(nw.EnableICC)
		options["com.docker.network.bridge.enable_ip_masquerade"] = "true"
		options["com.docker.network.bridge.host_binding_ipv4"] = "0.0.0.0"
		options["com.docker.network.bridge.name"] = "pterodactyl0"
		options["com.docker.network.driver.mtu"] = "1500"
	}

	if nw.Driver == "overlay" {
		options["encryption"] = "true"
		options["com.docker.network.driver.mtu"] = "9216"
	}

	if nw.Driver == "macvlan" {
		options["parent"] = config.Get().Docker.Network.Interface
	}

	_, err := cli.NetworkCreate(ctx, nw.Name, types.NetworkCreate{
		Driver:     nw.Driver,
		EnableIPv6: true,
		Internal:   nw.IsInternal,
		IPAM: &network.IPAM{
			Config: []network.IPAMConfig{{
				Subnet:  nw.Interfaces.V4.Subnet,
				Gateway: nw.Interfaces.V4.Gateway,
			}, {
				Subnet:  nw.Interfaces.V6.Subnet,
				Gateway: nw.Interfaces.V6.Gateway,
			}},
		},
		Options: options,
	})
	if err != nil {
		log.Error(fmt.Sprintf("Error creating network %s driver:%s", nw.Name, nw.Driver))
		return err
	}
	if nw.Driver != "host" && nw.Driver != "overlay" && nw.Driver != "weavemesh" && nw.Driver != "macvlan" {
		config.Update(func(c *config.Configuration) {
			c.Docker.Network.Interface = c.Docker.Network.Interfaces.V4.Gateway
		})
	}
	return nil
}
