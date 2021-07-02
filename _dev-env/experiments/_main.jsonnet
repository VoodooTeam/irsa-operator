local experiments = {
  policyRecreates: import './policy-recreation.libsonnet',
};

{
  [e + '.json']: experiments[e]
  for e in std.objectFields(experiments)
}
