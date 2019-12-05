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
	"reflect"

	extensionsobj "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
)

var CRD = &extensionsobj.CustomResourceDefinition{
	ObjectMeta: metav1.ObjectMeta{
		Name: "cronhpas.extensions.tkestack.io",
	},
	TypeMeta: metav1.TypeMeta{
		Kind:       "CustomResourceDefinition",
		APIVersion: "apiextensions.k8s.io/v1beta1",
	},
	Spec: extensionsobj.CustomResourceDefinitionSpec{
		Group:   "extensions.tkestack.io",
		Version: "v1",
		Scope:   extensionsobj.ResourceScope("Namespaced"),
		Names: extensionsobj.CustomResourceDefinitionNames{
			Plural:   "cronhpas",
			Singular: "cronhpa",
			Kind:     "CronHPA",
			ListKind: "CronHPAList",
		},
	},
}

func EnsureCRDCreated(client apiextensionsclient.Interface) (created bool, err error) {
	crdClient := client.ApiextensionsV1beta1().CustomResourceDefinitions()
	presetCRD, err := crdClient.Get(CRD.Name, metav1.GetOptions{})
	if err == nil {
		if reflect.DeepEqual(presetCRD.Spec, CRD.Spec) {
			klog.V(1).Infof("CRD %s already exists", CRD.Name)
		} else {
			klog.V(3).Infof("Update CRD %s: %+v -> %+v", CRD.Name, presetCRD.Spec, CRD.Spec)
			newCRD := CRD
			newCRD.ResourceVersion = presetCRD.ResourceVersion
			// Update CRD
			if _, err := crdClient.Update(newCRD); err != nil {
				klog.Errorf("Error update CRD %s: %v", CRD.Name, err)
				return false, err
			}
			klog.V(1).Infof("Update CRD %s successfully.", CRD.Name)
		}
	} else {
		// If not exist, create a new one
		if _, err := crdClient.Create(CRD); err != nil {
			klog.Errorf("Error creating CRD %s: %v", CRD.Name, err)
			return false, err
		}
		klog.V(1).Infof("Create CRD %s successfully.", CRD.Name)
	}

	return true, nil
}
