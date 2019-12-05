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

package cronhpa

import (
	"fmt"
	"time"

	"tkestack.io/cron-hpa/pkg/apis/cronhpacontroller/v1"
	clientset "tkestack.io/cron-hpa/pkg/client/clientset/versioned"
	cronhpascheme "tkestack.io/cron-hpa/pkg/client/clientset/versioned/scheme"

	cronutil "github.com/robfig/cron"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	cacheddiscovery "k8s.io/client-go/discovery/cached"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/restmapper"
	scaleclient "k8s.io/client-go/scale"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"
	controllerpkg "k8s.io/kubernetes/pkg/controller"
)

const controllerAgentName = "cronhpa-controller"

// Controller is the controller implementation for cronhpa resources
type Controller struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface
	// cronhpaclientset is a clientset for our own API group
	cronhpaclientset clientset.Interface

	restMapper      *restmapper.DeferredDiscoveryRESTMapper
	scaleNamespacer scaleclient.ScalesGetter

	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	recorder record.EventRecorder
}

// NewController returns a new cronhpa controller
func NewController(
	kubeclientset kubernetes.Interface,
	cronhpaclientset clientset.Interface,
	rootClientBuilder controllerpkg.ControllerClientBuilder) (*Controller, error) {

	// Create event broadcaster
	// Add cronhpa-controller types to the default Kubernetes Scheme so Events can be
	// logged for cronhpa-controller types.
	cronhpascheme.AddToScheme(scheme.Scheme)
	klog.V(4).Info("Creating event broadcaster")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	// Use a discovery client capable of being refreshed.
	discoveryClient := rootClientBuilder.ClientOrDie("cron-hpa-controller")
	cronhpaClientConfig := rootClientBuilder.ConfigOrDie("cron-hpa-controller")

	cachedClient := cacheddiscovery.NewMemCacheClient(discoveryClient.Discovery())
	restMapper := restmapper.NewDeferredDiscoveryRESTMapper(cachedClient)
	//restMapper := discovery.NewDeferredDiscoveryRESTMapper(cachedDiscovery, apimeta.InterfacesForUnstructured)
	restMapper.Reset()
	// we don't use cached discovery because DiscoveryScaleKindResolver does its own caching,
	// so we want to re-fetch every time when we actually ask for it
	scaleKindResolver := scaleclient.NewDiscoveryScaleKindResolver(discoveryClient.Discovery())
	scaleClient, err := scaleclient.NewForConfig(cronhpaClientConfig, restMapper, dynamic.LegacyAPIPathResolverFunc, scaleKindResolver)
	if err != nil {
		return nil, err
	}

	controller := &Controller{
		kubeclientset:    kubeclientset,
		cronhpaclientset: cronhpaclientset,
		restMapper:       restMapper,
		scaleNamespacer:  scaleClient,
		recorder:         recorder,
	}

	return controller, nil
}

func (c *Controller) Run(stopCh <-chan struct{}) error {
	defer runtime.HandleCrash()

	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting cronhpa controller")

	go wait.Until(c.syncAll, 10*time.Second, stopCh)
	go wait.Until(func() { c.restMapper.Reset() }, 30*time.Second, stopCh)
	<-stopCh
	klog.Info("Shutting down")

	return nil
}

func (c *Controller) GetEventRecorder() record.EventRecorder {
	return c.recorder
}

func (c *Controller) syncAll() {
	klog.V(4).Infof("Starting sync all")
	cronhpas, err := c.cronhpaclientset.CronhpacontrollerV1().CronHPAs(metav1.NamespaceAll).List(metav1.ListOptions{})
	if err != nil {
		klog.Errorf("Failed to list cronhpas")
		return
	}
	for _, cronhpa := range cronhpas.Items {
		klog.V(4).Infof("Sync cronhpa: %s", cronhpa.Name)
		c.syncOne(&cronhpa)
	}
}

func (c *Controller) syncOne(cronhpa *v1.CronHPA) {
	now := time.Now()
	latestSchedledTime := getLatestScheduledTime(cronhpa)
	for _, cron := range cronhpa.Spec.Crons {
		sched, err := cronutil.ParseStandard(cron.Schedule)
		if err != nil {
			klog.Errorf("Unparseable schedule: %s : %s", cron.Schedule, err)
		}
		t := sched.Next(latestSchedledTime)
		klog.V(4).Infof("Next schedule for %s of cronhpa %s: %v", cron.Schedule, getCronHPAFullName(cronhpa), t)
		if !t.After(now) {
			klog.V(4).Infof("Scale %s to replicas %d for schedule %s", getCronHPAFullName(cronhpa),
				cron.TargetReplicas, cron.Schedule)
			// Set new replicas
			if err := c.scale(cronhpa, cron.TargetReplicas); err != nil {
				klog.Errorf("Failed to scale %s to replicas %d: %v", getCronHPAFullName(cronhpa), cron.TargetReplicas, err)
				return
			}
			// Update status
			cronhpa.Status.LastScheduleTime = &metav1.Time{Time: time.Now()}
			if _, err := c.cronhpaclientset.CronhpacontrollerV1().CronHPAs(cronhpa.Namespace).Update(cronhpa); err != nil {
				klog.Errorf("Failed to update cronhpa %s's LastScheduleTime(%+v): %v",
					getCronHPAFullName(cronhpa), cronhpa.Status.LastScheduleTime.Time, err)
			}

			return
		}
	}
}

func (c *Controller) scale(cronhpa *v1.CronHPA, replicas int32) error {
	reference := fmt.Sprintf("%s/%s/%s", cronhpa.Spec.ScaleTargetRef.Kind, cronhpa.Namespace, cronhpa.Spec.ScaleTargetRef.Name)

	targetGV, err := schema.ParseGroupVersion(cronhpa.Spec.ScaleTargetRef.APIVersion)
	if err != nil {
		c.recorder.Eventf(cronhpa, corev1.EventTypeWarning, "FailedGetScale", err.Error())
		return fmt.Errorf("invalid API version in scale target reference: %v", err)
	}

	targetGK := schema.GroupKind{
		Group: targetGV.Group,
		Kind:  cronhpa.Spec.ScaleTargetRef.Kind,
	}

	mappings, err := c.restMapper.RESTMappings(targetGK)
	if err != nil {
		c.recorder.Eventf(cronhpa, corev1.EventTypeWarning, "FailedGetScale", err.Error())
		return fmt.Errorf("unable to determine resource for scale target reference: %v", err)
	}

	scale, targetGR, err := c.scaleForResourceMappings(cronhpa.Namespace, cronhpa.Spec.ScaleTargetRef.Name, mappings)
	if err != nil {
		c.recorder.Eventf(cronhpa, corev1.EventTypeWarning, "FailedGetScale", err.Error())
		return fmt.Errorf("failed to query scale subresource for %s: %v", reference, err)
	}

	if scale.Spec.Replicas != replicas {
		oldReplicas := scale.Spec.Replicas
		scale.Spec.Replicas = replicas
		_, err = c.scaleNamespacer.Scales(cronhpa.Namespace).Update(targetGR, scale)
		if err != nil {
			c.recorder.Eventf(cronhpa, corev1.EventTypeWarning, "FailedRescale", err.Error())
			return fmt.Errorf("failed to rescale %s: %v", reference, err)
		}
		c.recorder.Eventf(cronhpa, corev1.EventTypeNormal, "SuccessfulRescale", "New size: %d", replicas)
		klog.Infof("Successful scale of %s, old size: %d, new size: %d",
			getCronHPAFullName(cronhpa), oldReplicas, replicas)
	} else {
		klog.V(4).Infof("No need to scale %s to %v, same replicas", getCronHPAFullName(cronhpa), replicas)
	}

	return nil
}

// scaleForResourceMappings attempts to fetch the scale for the
// resource with the given name and namespace, trying each RESTMapping
// in turn until a working one is found.  If none work, the first error
// is returned.  It returns both the scale, as well as the group-resource from
// the working mapping.
func (c *Controller) scaleForResourceMappings(namespace, name string, mappings []*apimeta.RESTMapping) (*autoscalingv1.Scale, schema.GroupResource, error) {
	var firstErr error
	for i, mapping := range mappings {
		targetGR := mapping.Resource.GroupResource()
		scale, err := c.scaleNamespacer.Scales(namespace).Get(targetGR, name)
		if err == nil {
			return scale, targetGR, nil
		}

		// if this is the first error, remember it,
		// then go on and try other mappings until we find a good one
		if i == 0 {
			firstErr = err
		}
	}

	// make sure we handle an empty set of mappings
	if firstErr == nil {
		firstErr = fmt.Errorf("unrecognized resource")
	}

	return nil, schema.GroupResource{}, firstErr
}

func getLatestScheduledTime(cronhpa *v1.CronHPA) time.Time {
	if cronhpa.Status.LastScheduleTime != nil {
		return cronhpa.Status.LastScheduleTime.Time
	} else {
		return cronhpa.CreationTimestamp.Time
	}
}

func getCronHPAFullName(cronhpa *v1.CronHPA) string {
	return cronhpa.Namespace + "/" + cronhpa.Name
}
