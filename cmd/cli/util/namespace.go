/*
Copyright 2025.

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

package util

import (
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/klog/v2"
)

func GetNamespace(cf *genericclioptions.ConfigFlags) string {
	if cf == nil {
		return "default"
	}
	if ns := cf.Namespace; ns != nil && *ns != "" {
		return *ns
	}

	clientConfig := cf.ToRawKubeConfigLoader()
	ns, _, err := clientConfig.Namespace()
	if err != nil {
		klog.Errorln(err)
		return "default"
	}
	return ns
}
