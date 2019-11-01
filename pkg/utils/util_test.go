/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package utils

import (
	crdv1 "github.com/kubernetes-csi/external-snapshotter/pkg/apis/volumesnapshot/v1beta1"
	"k8s.io/api/core/v1"
	//metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"testing"
)

func TestGetSecretReference(t *testing.T) {
	testcases := map[string]struct {
		params          map[string]string
		snapContentName string
		snapshot        *crdv1.VolumeSnapshot
		expectRef       *v1.SecretReference
		expectErr       bool
	}{
		"no params": {
			params:    nil,
			expectRef: nil,
		},
		"empty err": {
			params:    map[string]string{SnapshotterSecretNameKey: "", SnapshotterSecretNamespaceKey: ""},
			expectErr: true,
		},
		"[deprecated] name, no namespace": {
			params:    map[string]string{SnapshotterSecretNameKey: "foo"},
			expectErr: true,
		},
		"namespace, no name": {
			params:    map[string]string{prefixedSnapshotterSecretNamespaceKey: "foo"},
			expectErr: true,
		},
		"simple - valid": {
			params:    map[string]string{prefixedSnapshotterSecretNameKey: "name", prefixedSnapshotterSecretNamespaceKey: "ns"},
			snapshot:  &crdv1.VolumeSnapshot{},
			expectRef: &v1.SecretReference{Name: "name", Namespace: "ns"},
		},
		"[deprecated] simple - valid, no pvc": {
			params:    map[string]string{SnapshotterSecretNameKey: "name", SnapshotterSecretNamespaceKey: "ns"},
			snapshot:  nil,
			expectRef: &v1.SecretReference{Name: "name", Namespace: "ns"},
		},
		"simple - invalid name": {
			params:    map[string]string{prefixedSnapshotterSecretNameKey: "bad name", prefixedSnapshotterSecretNamespaceKey: "ns"},
			snapshot:  &crdv1.VolumeSnapshot{},
			expectRef: nil,
			expectErr: true,
		},
		"[deprecated] simple - invalid namespace": {
			params:    map[string]string{SnapshotterSecretNameKey: "name", SnapshotterSecretNamespaceKey: "bad ns"},
			snapshot:  &crdv1.VolumeSnapshot{},
			expectRef: nil,
			expectErr: true,
		},
		"template - invalid namespace tokens": {
			params: map[string]string{
				SnapshotterSecretNameKey:      "myname",
				SnapshotterSecretNamespaceKey: "mynamespace${bar}",
			},
			snapshot:  &crdv1.VolumeSnapshot{},
			expectRef: nil,
			expectErr: true,
		},
		"template - invalid name tokens": {
			params: map[string]string{
				SnapshotterSecretNameKey:      "myname${foo}",
				SnapshotterSecretNamespaceKey: "mynamespace",
			},
			snapshot:  &crdv1.VolumeSnapshot{},
			expectRef: nil,
			expectErr: true,
		},
	}

	for k, tc := range testcases {
		t.Run(k, func(t *testing.T) {
			ref, err := GetSecretReference(tc.params, tc.snapContentName, tc.snapshot)
			if err != nil {
				if tc.expectErr {
					return
				}
				t.Fatalf("Did not expect error but got: %v", err)

			} else {
				if tc.expectErr {
					t.Fatalf("Expected error but got none")
				}
			}
			if !reflect.DeepEqual(ref, tc.expectRef) {
				t.Errorf("Expected %v, got %v", tc.expectRef, ref)
			}
		})
	}
}

func TestRemovePrefixedCSIParams(t *testing.T) {
	testcases := []struct {
		name           string
		params         map[string]string
		expectedParams map[string]string
		expectErr      bool
	}{
		{
			name:           "no prefix",
			params:         map[string]string{"csiFoo": "bar", "bim": "baz"},
			expectedParams: map[string]string{"csiFoo": "bar", "bim": "baz"},
		},
		{
			name:           "one prefixed",
			params:         map[string]string{prefixedSnapshotterSecretNameKey: "bar", "bim": "baz"},
			expectedParams: map[string]string{"bim": "baz"},
		},
		{
			name: "all known prefixed",
			params: map[string]string{
				prefixedSnapshotterSecretNameKey:      "csiBar",
				prefixedSnapshotterSecretNamespaceKey: "csiBar",
			},
			expectedParams: map[string]string{},
		},
		{
			name: "all known deprecated params not stripped",
			params: map[string]string{
				SnapshotterSecretNameKey:      "csiBar",
				SnapshotterSecretNamespaceKey: "csiBar",
			},
			expectedParams: map[string]string{
				SnapshotterSecretNameKey:      "csiBar",
				SnapshotterSecretNamespaceKey: "csiBar",
			},
		},
		{
			name:      "unknown prefixed var",
			params:    map[string]string{csiParameterPrefix + "bim": "baz"},
			expectErr: true,
		},
		{
			name:           "empty",
			params:         map[string]string{},
			expectedParams: map[string]string{},
		},
	}
	for _, tc := range testcases {
		t.Logf("test: %v", tc.name)
		newParams, err := RemovePrefixedParameters(tc.params)
		if err != nil {
			if tc.expectErr {
				continue
			} else {
				t.Fatalf("Encountered unexpected error: %v", err)
			}
		} else {
			if tc.expectErr {
				t.Fatalf("Did not get error when one was expected")
			}
		}
		eq := reflect.DeepEqual(newParams, tc.expectedParams)
		if !eq {
			t.Fatalf("Stripped paramaters: %v not equal to expected paramaters: %v", newParams, tc.expectedParams)
		}
	}
}
