{
  policy:: {
    group: 'irsa.voodoo.io',
    version: 'v1alpha1',
    plural: 'policies',
    name: 's3put',
    ns: 'default',
  },

  policyIsPresent:: {
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
      arguments: $.policy,
    },
  },

  operatorHealthy:: {
    name: 'pods-in-conditions',
    type: 'probe',
    tolerance: true,
    provider: {
      type: 'python',
      module: 'chaosk8s.pod.probes',
      func: 'pods_in_conditions',
      arguments: {
        label_selector: 'app=irsa-operator',
        conditions: [{ type: 'Ready', status: 'True' }],
        ns: 'irsa-operator-system',
      },
    },
  },
}
