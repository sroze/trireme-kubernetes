package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/aporeto-inc/trireme-kubernetes/auth"
	"github.com/aporeto-inc/trireme-kubernetes/config"
	"github.com/aporeto-inc/trireme-kubernetes/exclusion"
	"github.com/aporeto-inc/trireme-kubernetes/resolver"

	"github.com/aporeto-inc/trireme"
	"github.com/aporeto-inc/trireme/configurator"
	"github.com/aporeto-inc/trireme/enforcer"
	"github.com/aporeto-inc/trireme/enforcer/tokens"
	"github.com/aporeto-inc/trireme/monitor"
	"github.com/aporeto-inc/trireme/supervisor"

	"github.com/golang/glog"
)

func main() {
	config := config.LoadConfig()

	glog.V(2).Infof("Config used: %+v ", config)

	// Create New PolicyEngine for  Kubernetes
	kubernetesPolicy, err := resolver.NewKubernetesPolicy(config.KubeConfigLocation, config.KubeNodeName)
	if err != nil {
		fmt.Printf("Error initializing KubernetesPolicy, exiting: %s \n", err)
		return
	}

	var trireme trireme.Trireme
	var monitor monitor.Monitor
	var excluder supervisor.Excluder
	var publicKeyAdder enforcer.PublicKeyAdder

	// Checking statically if the Node name is not more than the maximum ServerID supported in the token package.
	if len(config.KubeNodeName) > tokens.MaxServerName {
		config.KubeNodeName = config.KubeNodeName[:tokens.MaxServerName]
	}

	if config.AuthType == "PSK" {
		// Starting PSK
		glog.V(2).Infof("Starting Trireme PSK")
		trireme, monitor, excluder = configurator.NewPSKTriremeWithDockerMonitor(config.KubeNodeName, config.TriremeNets, kubernetesPolicy, nil, nil, config.ExistingContainerSync, []byte(config.TriremePSK))

	}
	if config.AuthType == "PKI" {
		// Starting PKI
		glog.V(2).Infof("Starting Trireme PKI")
		// Load the PKI Certs/Keys.
		pki, err := auth.LoadPKI(config.PKIDirectory)
		if err != nil {
			fmt.Printf("Error loading Certificates for PKI Trireme, exiting: %s \n", err)
			return
		}
		// Starting PKI
		trireme, monitor, excluder, publicKeyAdder = configurator.NewPKITriremeWithDockerMonitor(config.KubeNodeName, config.TriremeNets, kubernetesPolicy, nil, nil, config.ExistingContainerSync, pki.KeyPEM, pki.CertPEM, pki.CaCertPEM)

		// Sync the certs over all the Kubernetes Cluster.
		// 1) Adds the localCert on the localNode annotation
		// 2) Sync All the Certs from the other nodes to the CertCache (interface)
		// 3) Waits and listen for new nodes coming up.
		certs := auth.NewCertsWatcher(*kubernetesPolicy.KubernetesClient, publicKeyAdder, config.NodeAnnotationKey)
		certs.AddCertToNodeAnnotation(*kubernetesPolicy.KubernetesClient, pki.CertPEM)
		certs.SyncNodeCerts(*kubernetesPolicy.KubernetesClient)
		go certs.StartWatchingCerts()

	}
	// Register Trireme to the Policy.
	kubernetesPolicy.SetPolicyUpdater(trireme)
	// Register the IPExcluder to the Policy
	kubernetesPolicy.SetExcluder(excluder)

	exclusionWatcher, err := exclusion.NewWatcher(config.TriremeNets, *kubernetesPolicy.KubernetesClient, excluder)
	if err != nil {
		log.Fatalf("Error creating the exclusion Watcher: %s", err)
	}

	go exclusionWatcher.Start()

	// Start all the go routines.
	trireme.Start()
	monitor.Start()
	kubernetesPolicy.Run()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	<-c

	fmt.Println("Bye Kubernetes!")
	kubernetesPolicy.Stop()
	monitor.Stop()
	trireme.Stop()

}
