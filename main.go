/*
 * Tencent is pleased to support the open source community by making TKEStack available.
 *
 * Copyright (C) 2012-2019 Tencent. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License"); you may not use
 * this file except in compliance with the License. You may obtain a copy of the
 * License at
 *
 * https://opensource.org/licenses/Apache-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
 * WARRANTIES OF ANY KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations under the License.
 */

package main

import (
	"context"
	"flag"
	"os"
	"time"

	"tkestack.io/cron-hpa/pkg/admission"
	clientset "tkestack.io/cron-hpa/pkg/client/clientset/versioned"
	"tkestack.io/cron-hpa/pkg/cronhpa"
	"tkestack.io/cron-hpa/pkg/logs"

	"github.com/spf13/pflag"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	apiserverconfig "k8s.io/apiserver/pkg/apis/config"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/client/leaderelectionconfig"
	controllerpkg "k8s.io/kubernetes/pkg/controller"
)

const (
	DefaultLeaseDuration = 15 * time.Second
	DefaultRenewDeadline = 10 * time.Second
	DefaultRetryPeriod   = 2 * time.Second
)

var (
	masterURL  string
	kubeconfig string
	createCRD  bool
	// kubeAPIQPS is the QPS to use while talking with kubernetes apiserver.
	kubeAPIQPS float32
	// kubeAPIBurst is the burst to use while talking with kubernetes apiserver.
	kubeAPIBurst int
	// leaderElection defines the configuration of leader election client.
	leaderElection = apiserverconfig.LeaderElectionConfiguration{
		LeaderElect:   false,
		LeaseDuration: metav1.Duration{Duration: DefaultLeaseDuration},
		RenewDeadline: metav1.Duration{Duration: DefaultRenewDeadline},
		RetryPeriod:   metav1.Duration{Duration: DefaultRetryPeriod},
		ResourceLock:  resourcelock.EndpointsResourceLock,
	}

	// Admission related config
	registerAdmission bool
	tlsCAfile         string
	tlsCertFile       string
	tlsKeyFile        string
	listenAddress     string
	// namespace to deploy CronHPA controller
	namespace string
)

func main() {
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	addFlags(pflag.CommandLine)
	pflag.Parse()

	logs.InitLogs()
	defer logs.FlushLogs()

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s", err.Error())
	}
	cfg.QPS = kubeAPIQPS
	cfg.Burst = kubeAPIBurst

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building kubernetes clientset: %s", err.Error())
	}

	cronhpaClient, err := clientset.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building example clientset: %s", err.Error())
	}

	extensionsClient, err := apiextensionsclient.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error instantiating apiextensions client: %s", err.Error())
	}

	rootClientBuilder := controllerpkg.SimpleControllerClientBuilder{
		ClientConfig: cfg,
	}

	controller, err := cronhpa.NewController(kubeClient, cronhpaClient, rootClientBuilder)
	if err != nil {
		klog.Fatalf("Failed to new controller: %s", err)
	}

	run := func(ctx context.Context) {
		if createCRD {
			wait.PollUntil(time.Second*5, func() (bool, error) { return cronhpa.EnsureCRDCreated(extensionsClient) }, ctx.Done())
		}

		if registerAdmission {
			wait.PollImmediateUntil(time.Second*5, func() (bool, error) {
				return admission.Register(kubeClient, namespace, tlsCAfile)
			}, ctx.Done())
			server, err := admission.NewServer(listenAddress, tlsCertFile, tlsKeyFile)
			if err != nil {
				klog.Fatalf("Error new admission server: %v", err)
			}
			go server.Run(ctx.Done())
		}

		if err = controller.Run(ctx.Done()); err != nil {
			klog.Fatalf("Error running controller: %s", err.Error())
		}
	}

	if !leaderElection.LeaderElect {
		run(context.Background())
		panic("unreachable")
	}

	id, err := os.Hostname()
	if err != nil {
		klog.Fatalf("Failed to get hostname: %s", err.Error())
	}

	leaderElectionClient := kubernetes.NewForConfigOrDie(restclient.AddUserAgent(cfg, "cron-hpa-leader-election"))
	rl, err := resourcelock.New(leaderElection.ResourceLock,
		"kube-system",
		"cron-hpa-controller",
		leaderElectionClient.CoreV1(),
		resourcelock.ResourceLockConfig{
			Identity:      id,
			EventRecorder: controller.GetEventRecorder(),
		})
	if err != nil {
		klog.Fatalf("error creating lock: %v", err)
	}

	leaderelection.RunOrDie(context.Background(), leaderelection.LeaderElectionConfig{
		Lock:          rl,
		LeaseDuration: leaderElection.LeaseDuration.Duration,
		RenewDeadline: leaderElection.RenewDeadline.Duration,
		RetryPeriod:   leaderElection.RetryPeriod.Duration,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: run,
			OnStoppedLeading: func() {
				klog.Fatalf("leaderelection lost")
			},
		},
	})
	panic("unreachable")
}

func addFlags(fs *pflag.FlagSet) {
	fs.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	fs.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
	fs.BoolVar(&createCRD, "create-crd", true, "Create cronhpa CRD if it does not exist")
	fs.Float32Var(&kubeAPIQPS, "kube-api-qps", kubeAPIQPS, "QPS to use while talking with kubernetes apiserver")
	fs.IntVar(&kubeAPIBurst, "kube-api-burst", kubeAPIBurst, "Burst to use while talking with kubernetes apiserver")

	// Admission related
	fs.BoolVar(&registerAdmission, "register-admission", false, "Register admission for CronHPA controller")
	fs.StringVar(&tlsCAfile, "tlsCAFile", "/etc/certs/ca.crt", "File containing the x509 CA for HTTPS")
	fs.StringVar(&listenAddress, "listen-address", ":8443", "The address to listen on for HTTP requests.")
	fs.StringVar(&tlsCertFile, "tlsCertFile", "/etc/certs/tls.crt", "File containing the x509 Certificate for HTTPS.")
	fs.StringVar(&tlsKeyFile, "tlsKeyFile", "/etc/certs/tls.key", "File containing the x509 private key to for HTTPS.")
	fs.StringVar(&namespace, "namespace", "kube-system", "Namespace to deploy tapp controller")

	leaderelectionconfig.BindFlags(&leaderElection, fs)
}
