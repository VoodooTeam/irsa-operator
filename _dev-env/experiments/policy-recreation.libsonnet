local policy =
  {
    group: 'irsa.voodoo.io',
    version: 'v1alpha1',
    plural: 'policies',
    name: 's3put',
    ns: 'default',
  };

local policyIsPresent = {
  name: 'get-custom-object',
  type: 'probe',
  tolerance: {
    type: 'jsonpath',
    path: '$.apiVersion',
  },
  provider: {
    type: 'python',
    module: 'chaosk8s.crd.probes',
    func: 'get_custom_object',
    arguments: policy,
  },
};

{
  version: '1.0.0',
  title: 'Policy Deletion',
  description: "We expect that when a policy is deleted, it's automatically recreated",
  tags: [],
  'steady-state-hypothesis': {
    title: 'the policiy is present',
    probes: [policyIsPresent],
  },
  method: [
    {
      name: 'delete-custom-object',
      type: 'action',
      provider: {
        type: 'python',
        module: 'chaosk8s.crd.actions',
        func: 'delete_custom_object',
        arguments: policy,
      },
    },
    policyIsPresent,
  ],
}
