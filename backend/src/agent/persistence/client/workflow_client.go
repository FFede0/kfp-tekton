// Copyright 2018 The Kubeflow Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package client

import (
	"context"
	"fmt"

	"github.com/kubeflow/pipelines/backend/src/common/util"
	wfapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	wfclientset "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	"github.com/tektoncd/pipeline/pkg/client/informers/externalversions/pipeline/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/tools/cache"
)

type WorkflowClientInterface interface {
	Get(namespace string, name string) (wf *util.Workflow, err error)
}

// WorkflowClient is a client to call the Workflow API.
type WorkflowClient struct {
	informer  v1beta1.PipelineRunInformer
	clientset *wfclientset.Clientset
}

// NewWorkflowClient creates an instance of the WorkflowClient.
func NewWorkflowClient(informer v1beta1.PipelineRunInformer,
	clientset *wfclientset.Clientset) *WorkflowClient {

	return &WorkflowClient{
		informer:  informer,
		clientset: clientset,
	}
}

// AddEventHandler adds an event handler.
func (c *WorkflowClient) AddEventHandler(funcs *cache.ResourceEventHandlerFuncs) {
	c.informer.Informer().AddEventHandler(funcs)
}

// HasSynced returns true if the shared informer's store has synced.
func (c *WorkflowClient) HasSynced() func() bool {
	return c.informer.Informer().HasSynced
}

// Get returns a Workflow, given a namespace and name.
func (c *WorkflowClient) Get(namespace string, name string) (
	wf *util.Workflow, err error) {
	workflow, err := c.informer.Lister().PipelineRuns(namespace).Get(name)
	if err != nil {
		var code util.CustomCode
		if util.IsNotFound(err) {
			code = util.CUSTOM_CODE_NOT_FOUND
		} else {
			code = util.CUSTOM_CODE_GENERIC
		}
		return nil, util.NewCustomError(err, code,
			"Error retrieving workflow (%v) in namespace (%v): %v", name, namespace, err)
	}
	if workflow.Status.ChildReferences != nil {
		hasTaskRun, hasRun := false, false
		for _, child := range workflow.Status.ChildReferences {
			switch child.Kind {
			case "TaskRun":
				hasTaskRun = true
			case "Run":
				hasRun = true
			default:
			}
		}
		// TODO: restruct the workflow to contain taskrun/run status, these 2 field
		// will be removed in the future
		if hasTaskRun {
			// fetch taskrun status and insert into Status.TaskRuns
			taskruns, err := c.clientset.TektonV1beta1().TaskRuns(namespace).List(context.Background(), v1.ListOptions{
				LabelSelector: fmt.Sprintf("%s=%s", util.LabelKeyWorkflowRunId, workflow.Labels[util.LabelKeyWorkflowRunId]),
			})
			if err != nil {
				return nil, util.NewInternalServerError(err, "can't fetch taskruns")
			}

			taskrunStatuses := make(map[string]*wfapi.PipelineRunTaskRunStatus, len(taskruns.Items))
			for _, taskrun := range taskruns.Items {
				taskrunStatuses[taskrun.Name] = &wfapi.PipelineRunTaskRunStatus{
					PipelineTaskName: taskrun.Labels["tekton.dev/pipelineTask"],
					Status:           taskrun.Status.DeepCopy(),
				}
			}
			workflow.Status.TaskRuns = taskrunStatuses
		}
		if hasRun {
			runs, err := c.clientset.TektonV1alpha1().Runs(namespace).List(context.Background(), v1.ListOptions{
				LabelSelector: fmt.Sprintf("%s=%s", util.LabelKeyWorkflowRunId, workflow.Labels[util.LabelKeyWorkflowRunId]),
			})
			if err != nil {
				return nil, util.NewInternalServerError(err, "can't fetch runs")
			}
			runStatuses := make(map[string]*wfapi.PipelineRunRunStatus, len(runs.Items))
			for _, run := range runs.Items {
				runStatuses[run.Name] = &wfapi.PipelineRunRunStatus{
					PipelineTaskName: run.Labels["tekton.dev/pipelineTask"],
					Status:           run.Status.DeepCopy(),
				}
			}
			workflow.Status.Runs = runStatuses
		}
	}

	return util.NewWorkflow(workflow), nil
}
