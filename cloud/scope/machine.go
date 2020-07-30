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

package scope

import (
	"context"
	"encoding/base64"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/klog/klogr"
	infrav1 "sigs.k8s.io/cluster-api-provider-azure/api/v1alpha3"
	azure "sigs.k8s.io/cluster-api-provider-azure/cloud"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	"sigs.k8s.io/cluster-api/controllers/noderefutil"
	capierrors "sigs.k8s.io/cluster-api/errors"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MachineScopeParams defines the input parameters used to create a new MachineScope.
type MachineScopeParams struct {
	Client           client.Client
	Logger           logr.Logger
	ClusterDescriber azure.ClusterDescriber
	Machine          *clusterv1.Machine
	AzureMachine     *infrav1.AzureMachine
}

// NewMachineScope creates a new MachineScope from the supplied parameters.
// This is meant to be called for each reconcile iteration.
func NewMachineScope(params MachineScopeParams) (*MachineScope, error) {
	if params.Client == nil {
		return nil, errors.New("client is required when creating a MachineScope")
	}
	if params.Machine == nil {
		return nil, errors.New("machine is required when creating a MachineScope")
	}
	if params.AzureMachine == nil {
		return nil, errors.New("azure machine is required when creating a MachineScope")
	}
	if params.Logger == nil {
		params.Logger = klogr.New()
	}

	helper, err := patch.NewHelper(params.AzureMachine, params.Client)
	if err != nil {
		return nil, errors.Wrap(err, "failed to init patch helper")
	}
	return &MachineScope{
		client:           params.Client,
		Machine:          params.Machine,
		AzureMachine:     params.AzureMachine,
		Logger:           params.Logger,
		patchHelper:      helper,
		ClusterDescriber: params.ClusterDescriber,
	}, nil
}

// MachineScope defines a scope defined around a machine and its cluster.
type MachineScope struct {
	logr.Logger
	client      client.Client
	patchHelper *patch.Helper

	azure.ClusterDescriber
	Machine      *clusterv1.Machine
	AzureMachine *infrav1.AzureMachine
}

// VMSpecs returns the VM specs.
func (m *MachineScope) VMSpecs() []azure.VMSpec {
	return []azure.VMSpec{
		{
			Name:                   m.Name(),
			Role:                   m.Role(),
			NICNames:               m.NICNames(),
			SSHKeyData:             m.AzureMachine.Spec.SSHPublicKey,
			Size:                   m.AzureMachine.Spec.VMSize,
			OSDisk:                 m.AzureMachine.Spec.OSDisk,
			DataDisks:              m.AzureMachine.Spec.DataDisks,
			Zone:                   m.AvailabilityZone(),
			Identity:               m.AzureMachine.Spec.Identity,
			UserAssignedIdentities: m.AzureMachine.Spec.UserAssignedIdentities,
			SpotVMOptions:          m.AzureMachine.Spec.SpotVMOptions,
		},
	}
}

// PublicIPSpec returns the public IP specs.
func (m *MachineScope) PublicIPSpecs() []azure.PublicIPSpec {
	var spec []azure.PublicIPSpec
	if m.AzureMachine.Spec.AllocatePublicIP == true {
		spec = append(spec, azure.PublicIPSpec{
			Name: azure.GenerateNodePublicIPName(m.Name()),
		})
	}
	return spec
}

// InboundNatSpecs returns the inbound NAT specs.
func (m *MachineScope) InboundNatSpecs() []azure.InboundNatSpec {
	if m.Role() == infrav1.ControlPlane {
		return []azure.InboundNatSpec{
			{
				Name:             m.Name(),
				LoadBalancerName: azure.GeneratePublicLBName(m.ClusterName()),
			},
		}
	}
	return []azure.InboundNatSpec{}
}

// NICSpecs returns the network interface specs.
func (m *MachineScope) NICSpecs() []azure.NICSpec {
	spec := azure.NICSpec{
		Name:                  azure.GenerateNICName(m.Name()),
		MachineName:           m.Name(),
		VNetName:              m.Vnet().Name,
		VNetResourceGroup:     m.Vnet().ResourceGroup,
		SubnetName:            m.Subnet().Name,
		VMSize:                m.AzureMachine.Spec.VMSize,
		AcceleratedNetworking: m.AzureMachine.Spec.AcceleratedNetworking,
	}
	if m.Role() == infrav1.ControlPlane {
		publicLBName := azure.GeneratePublicLBName(m.ClusterName())
		spec.PublicLBName = publicLBName
		spec.PublicLBAddressPoolName = azure.GenerateBackendAddressPoolName(publicLBName)
		spec.PublicLBNATRuleName = m.Name()
		internalLBName := azure.GenerateInternalLBName(m.ClusterName())
		spec.InternalLBName = internalLBName
		spec.InternalLBAddressPoolName = azure.GenerateBackendAddressPoolName(internalLBName)
	} else if m.Role() == infrav1.Node {
		publicLBName := m.ClusterName()
		spec.PublicLBName = publicLBName
		spec.PublicLBAddressPoolName = azure.GenerateOutboundBackendddressPoolName(publicLBName)
	}
	specs := []azure.NICSpec{spec}
	if m.AzureMachine.Spec.AllocatePublicIP == true {
		specs = append(specs, azure.NICSpec{
			Name:                  azure.GeneratePublicNICName(m.Name()),
			MachineName:           m.Name(),
			VNetName:              m.Vnet().Name,
			VNetResourceGroup:     m.Vnet().ResourceGroup,
			SubnetName:            m.Subnet().Name,
			PublicIPName:          azure.GenerateNodePublicIPName(m.Name()),
			VMSize:                m.AzureMachine.Spec.VMSize,
			AcceleratedNetworking: m.AzureMachine.Spec.AcceleratedNetworking,
		})
	}

	return specs
}

func (m *MachineScope) NICNames() []string {
	nicNames := make([]string, len(m.NICSpecs()))
	for i, nic := range m.NICSpecs() {
		nicNames[i] = nic.Name
	}
	return nicNames
}

// DiskSpecs returns the disk specs.
func (m *MachineScope) DiskSpecs() []azure.DiskSpec {
	spec := azure.DiskSpec{
		Name: azure.GenerateOSDiskName(m.Name()),
	}

	return []azure.DiskSpec{spec}
}

// RoleAssignmentSpecs returns the role assignment specs.
func (m *MachineScope) RoleAssignmentSpecs() []azure.RoleAssignmentSpec {
	if m.AzureMachine.Spec.Identity == infrav1.VMIdentitySystemAssigned {
		return []azure.RoleAssignmentSpec{
			{
				MachineName: m.Name(),
				UUID:        string(uuid.NewUUID()),
			},
		}
	}
	return []azure.RoleAssignmentSpec{}
}

// Subnet returns the machine's subnet based on its role
func (m *MachineScope) Subnet() *infrav1.SubnetSpec {
	if m.IsControlPlane() {
		return m.ControlPlaneSubnet()
	}
	return m.NodeSubnet()
}

// AvailabilityZone returns the AzureMachine Availability Zone.
// Priority for selecting the AZ is
//   1) Machine.Spec.FailureDomain
//   2) AzureMachine.Spec.FailureDomain (This is to support deprecated AZ)
//   3) AzureMachine.Spec.AvailabilityZone.ID (This is DEPRECATED)
//   4) No AZ
func (m *MachineScope) AvailabilityZone() string {
	if m.Machine.Spec.FailureDomain != nil {
		return *m.Machine.Spec.FailureDomain
	}
	// DEPRECATED: to support old clients
	if m.AzureMachine.Spec.FailureDomain != nil {
		return *m.AzureMachine.Spec.FailureDomain
	}
	if m.AzureMachine.Spec.AvailabilityZone.ID != nil {
		return *m.AzureMachine.Spec.AvailabilityZone.ID
	}

	return ""
}

// Name returns the AzureMachine name.
func (m *MachineScope) Name() string {
	return m.AzureMachine.Name
}

// Namespace returns the namespace name.
func (m *MachineScope) Namespace() string {
	return m.AzureMachine.Namespace
}

// IsControlPlane returns true if the machine is a control plane.
func (m *MachineScope) IsControlPlane() bool {
	return util.IsControlPlaneMachine(m.Machine)
}

// Role returns the machine role from the labels.
func (m *MachineScope) Role() string {
	if util.IsControlPlaneMachine(m.Machine) {
		return infrav1.ControlPlane
	}
	return infrav1.Node
}

// GetVMID returns the AzureMachine instance id by parsing Spec.ProviderID.
func (m *MachineScope) GetVMID() string {
	parsed, err := noderefutil.NewProviderID(m.GetProviderID())
	if err != nil {
		return ""
	}
	return parsed.ID()
}

// GetProviderID returns the AzureMachine providerID from the spec.
func (m *MachineScope) GetProviderID() string {
	if m.AzureMachine.Spec.ProviderID != nil {
		return *m.AzureMachine.Spec.ProviderID
	}
	return ""
}

// SetProviderID sets the AzureMachine providerID in spec.
func (m *MachineScope) SetProviderID(v string) {
	m.AzureMachine.Spec.ProviderID = to.StringPtr(v)
}

// GetVMState returns the AzureMachine VM state.
func (m *MachineScope) GetVMState() infrav1.VMState {
	if m.AzureMachine.Status.VMState != nil {
		return *m.AzureMachine.Status.VMState
	}
	return ""
}

// SetVMState sets the AzureMachine VM state.
func (m *MachineScope) SetVMState(v infrav1.VMState) {
	m.AzureMachine.Status.VMState = &v
}

// SetReady sets the AzureMachine Ready Status to true.
func (m *MachineScope) SetReady() {
	m.AzureMachine.Status.Ready = true
}

// SetNotReady sets the AzureMachine Ready Status to false.
func (m *MachineScope) SetNotReady() {
	m.AzureMachine.Status.Ready = false
}

// SetFailureMessage sets the AzureMachine status failure message.
func (m *MachineScope) SetFailureMessage(v error) {
	m.AzureMachine.Status.FailureMessage = to.StringPtr(v.Error())
}

// SetFailureReason sets the AzureMachine status failure reason.
func (m *MachineScope) SetFailureReason(v capierrors.MachineStatusError) {
	m.AzureMachine.Status.FailureReason = &v
}

// SetAnnotation sets a key value annotation on the AzureMachine.
func (m *MachineScope) SetAnnotation(key, value string) {
	if m.AzureMachine.Annotations == nil {
		m.AzureMachine.Annotations = map[string]string{}
	}
	m.AzureMachine.Annotations[key] = value
}

// SetAddresses sets the Azure address status.
func (m *MachineScope) SetAddresses(addrs []corev1.NodeAddress) {
	m.AzureMachine.Status.Addresses = addrs
}

// PatchObject persists the machine spec and status.
func (m *MachineScope) PatchObject(ctx context.Context) error {
	return m.patchHelper.Patch(ctx, m.AzureMachine)
}

// Close the MachineScope by updating the machine spec, machine status.
func (m *MachineScope) Close(ctx context.Context) error {
	return m.patchHelper.Patch(ctx, m.AzureMachine)
}

// AdditionalTags merges AdditionalTags from the scope's AzureCluster and AzureMachine. If the same key is present in both,
// the value from AzureMachine takes precedence.
func (m *MachineScope) AdditionalTags() infrav1.Tags {
	tags := make(infrav1.Tags)
	// Start with the cluster-wide tags...
	tags.Merge(m.ClusterDescriber.AdditionalTags())
	// ... and merge in the Machine's
	tags.Merge(m.AzureMachine.Spec.AdditionalTags)
	// Set the cloud provider tag
	tags[infrav1.ClusterAzureCloudProviderTagKey(m.ClusterName())] = string(infrav1.ResourceLifecycleOwned)

	return tags
}

// GetBootstrapData returns the bootstrap data from the secret in the Machine's bootstrap.dataSecretName.
func (m *MachineScope) GetBootstrapData(ctx context.Context) (string, error) {
	if m.Machine.Spec.Bootstrap.DataSecretName == nil {
		return "", errors.New("error retrieving bootstrap data: linked Machine's bootstrap.dataSecretName is nil")
	}
	secret := &corev1.Secret{}
	key := types.NamespacedName{Namespace: m.Namespace(), Name: *m.Machine.Spec.Bootstrap.DataSecretName}
	if err := m.client.Get(ctx, key, secret); err != nil {
		return "", errors.Wrapf(err, "failed to retrieve bootstrap data secret for AzureMachine %s/%s", m.Namespace(), m.Name())
	}

	value, ok := secret.Data["value"]
	if !ok {
		return "", errors.New("error retrieving bootstrap data: secret value key is missing")
	}
	return base64.StdEncoding.EncodeToString(value), nil
}

// Pick image from the machine configuration, or use a default one.
func (m *MachineScope) GetVMImage() (*infrav1.Image, error) {
	// Use custom Marketplace image, Image ID or a Shared Image Gallery image if provided
	if m.AzureMachine.Spec.Image != nil {
		return m.AzureMachine.Spec.Image, nil
	}
	m.Info("No image specified for machine, using default", "machine", m.AzureMachine.GetName())
	return azure.GetDefaultUbuntuImage(to.String(m.Machine.Spec.Version))
}
