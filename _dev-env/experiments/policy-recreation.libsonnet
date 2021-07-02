local s = import './shared.libsonnet';

{
  version: '1.0.0',
  title: 'Policy Deletion',
  description: "We expect that when a policy is deleted, it's automatically recreated",
  tags: [],
  'steady-state-hypothesis': {
    title: 'the policiy is present',
    probes: [
      s.policyIsPresent,
      s.operatorHealthy,
    ],
  },
  method: [
    {
      name: 'delete-custom-object',
      type: 'action',
      provider: {
        type: 'python',
        module: 'chaosk8s.crd.actions',
        func: 'delete_custom_object',
        arguments: s.policy,
      },
    },
    s.policyIsPresent,
    s.operatorHealthy,
  ],
}
