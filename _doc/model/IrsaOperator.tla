---- MODULE IrsaOperator ----
EXTENDS TLC, Naturals, Sequences

(* 
    Caveat :

    - distributed consensus is not displayed here (operator SDK handles this for us)
    - multi CR mechanism is not displayed here (simple scoping is enough to avoid collisions)
    - we also assume the specs are valid

*)

CONSTANTS
    NULL, \* dummy constant
    _workers \* the set of reconcile loops

VARIABLES 
    irsa, \* the iamroleserviceaccount CR
    policy, \* the policy CR
    role, \* the role CR
    sa, \* the serviceAccount to be created
    awsPolicy, \* the IAM policy on aws
    awsRole, \* the IAM role on aws
    wq, \* the k8s workqueue
    workers, \* the workers (concurrent reconcile loops)
    modified \* the k8s resources modified during an action (simulates the watch mechanism)

vars == <<irsa, policy, role, sa, awsPolicy, awsRole, wq, workers, modified>>

(* the different requests *)
iReq == "irsa"
pReq == "policy"
rReq == "role"
saReq == "sa"


pendingSt == "pending"

valid_states == {NULL, pendingSt}

TypeOk ==
    /\  irsa.st \in valid_states
    /\  policy.st \in valid_states 
    /\  role.st \in valid_states
    /\  sa.st \in valid_states
    /\ \A w \in DOMAIN workers: workers[w].req \in {NULL, iReq, pReq, rReq, saReq}
    /\  awsRole.arn \in {NULL, "roleARN"} 
    /\  awsPolicy.arn \in {NULL, "policyARN"} 


Init == 
    /\  irsa = [st |-> NULL,
                saName |-> "saName",
                stmt |-> "statement",
                roleARN |-> NULL,
                policyARN |-> NULL
                ]
    /\  policy = [st |-> NULL,
                  stmt |-> NULL,
                  awsPolicyArn |-> NULL
                ]
    /\  role = [st |-> NULL,
                saName |-> NULL,
                roleArn |-> NULL,
                policyArn |-> NULL,
                policiesAttached |-> FALSE 
                \* NB : this last flag is not yet in the implementation. 
                \* It's needed to avoid missing the attached policies
                ]
    /\  sa = [  st |-> NULL,
                name |-> NULL,
                roleArn |-> NULL
             ]
    /\  awsPolicy = [arn |-> NULL] \* union already created as expected & different
    /\  awsRole = [arn |-> NULL, attachedPolicy |-> NULL] \* union already created as expected & different
    /\  modified = <<iReq>>
    /\  wq = [dirty |-> {}, processing |-> {}, queue |-> <<>>] \* we start with an IrsaRequest in the dirty set
    /\  workers = [w \in _workers |-> [idle |-> TRUE, req |-> NULL]]



(***************************************************************************)
(* k8s workqueue                                                           *)
(***************************************************************************)

Enqueue(r) == \* sequence of modified resources, simulating the watch mechanism 
    /\ modified' = modified \o r

\* tla spec of the k8s workqueue algorithm
\* see : https://github.com/kubernetes/client-go/blob/a57d0056dbf1d48baaf3cee876c123bea745591f/util/workqueue/queue.go#L65
Add ==
    /\ modified # <<>>
    /\ modified' = Tail(modified)
    /\ LET e == Head(modified) IN
        IF e \in wq.dirty 
        THEN
            /\ UNCHANGED <<irsa, policy, role, sa, awsPolicy, awsRole, workers, wq>>
        ELSE
            /\ IF e \notin wq.processing
               THEN wq' = [wq EXCEPT !.dirty = wq.dirty \union {e}, !.queue = Append(wq.queue, e) ]
               ELSE wq' = [wq EXCEPT !.dirty = wq.dirty \union {e}]
            /\ UNCHANGED <<irsa, policy, role, sa, awsPolicy, awsRole, workers>>


Get(w) ==
    /\ workers[w].idle
    /\ workers[w].req = NULL
    /\ wq.queue # <<>>
    /\ LET head == Head(wq.queue) IN
        /\ workers' = [workers EXCEPT ![w] = [idle |-> FALSE, req |-> head]]
        /\ wq' = [wq EXCEPT !.queue = Tail(wq.queue), !.dirty = wq.dirty \ {head}, !.processing = wq.processing \union {head} ]
    /\ UNCHANGED <<awsPolicy, awsRole, irsa, modified, policy, role, sa>>


Done(w) ==
    /\ workers[w].idle
    /\ workers[w].req # NULL
    /\ workers' = [workers EXCEPT ![w] = [idle |-> TRUE, req |-> NULL]]
    /\ LET r == workers[w].req IN
        IF r \in wq.dirty 
        THEN wq' = [wq EXCEPT !.processing = wq.processing \ {r}, !.queue = Append(wq.queue, r)] 
        ELSE wq' = [wq EXCEPT !.processing = wq.processing \ {r} ]
    /\ UNCHANGED <<awsPolicy, awsRole, irsa, modified, policy, role, sa>>



(***************************************************************************)
(* the expected states when a resource has converged                       *)
(***************************************************************************)

IrsaComplete == 
    /\ policy.st # NULL
    /\ role.st # NULL
    /\ sa.st # NULL

policyComplete ==
    /\ policy.st # NULL
    /\ policy.stmt # NULL
    /\ policy.awsPolicyArn # NULL

roleComplete ==
    /\ role.st # NULL
    /\ role.saName # NULL
    /\ role.roleArn # NULL
    /\ role.policyArn # NULL
    /\ role.policiesAttached

saComplete == 
    /\ sa.st # NULL
    /\ sa.name # NULL
    /\ sa.roleArn # NULL



(***************************************************************************)
(* operator specific actions                                               *)
(***************************************************************************)

\* NB : update policy not displayed yet
CreatePolicy(w) ==
    \* irsa controller
    /\ workers[w].idle = FALSE
    /\ workers[w].req = iReq
    /\ policy.st = NULL \* policy doesn't exist
    /\ policy' = [policy EXCEPT !.st = "pending", !.stmt = irsa.stmt ]
    /\ workers' = [workers EXCEPT ![w].idle = TRUE]
    /\ Enqueue(<<pReq, iReq>>)
    /\ UNCHANGED <<awsPolicy, awsRole, irsa, role, sa, wq>>


CreateRole(w) ==
    \* irsa controller
    /\ workers[w].idle = FALSE
    /\ workers[w].req = iReq
    /\ role.st = NULL \* role doesn't exist
    /\ role' = [role EXCEPT !.st = "pending", !.saName = irsa.saName ]
    /\ workers' = [workers EXCEPT ![w].idle = TRUE]
    /\ Enqueue(<<rReq, iReq>>)
    /\ UNCHANGED <<awsPolicy, awsRole, irsa, policy, sa, wq>>


\* if it has one, we'll try to update it, not shown yet
PolicyHasNoARN(w) ==
    \* policy controller
    /\ workers[w].idle = FALSE
    /\ workers[w].req = pReq
    /\ policy.awsPolicyArn = NULL
    /\ IF awsPolicy.arn = NULL
        THEN /\ awsPolicy' = [awsPolicy EXCEPT !.arn = "policyARN"] 
             /\ Enqueue(<<pReq>>)
             /\ UNCHANGED <<awsRole, irsa, policy, role, sa, workers, wq>>
        ELSE /\ policy.awsPolicyArn = NULL 
             /\ policy' = [policy EXCEPT !.awsPolicyArn = awsPolicy.arn]
             /\ Enqueue(<<pReq>>)
             /\ UNCHANGED <<awsRole, awsPolicy, irsa, role, sa, workers, wq>>


RoleHasNoRoleARN(w) ==
    \* role controller
    /\ workers[w].idle = FALSE
    /\ workers[w].req = rReq
    /\ role.roleArn = NULL
    /\ IF awsRole.arn = NULL
        THEN /\ awsRole' = [awsRole EXCEPT !.arn = "roleARN"] 
             /\ UNCHANGED <<awsPolicy, irsa, policy, role, sa, workers, wq>>
        ELSE /\ role' = [role EXCEPT !.roleArn = awsRole.arn]
             /\ UNCHANGED <<awsPolicy, awsRole, awsPolicy, irsa, policy, sa, workers, wq>>
    /\ Enqueue(<<rReq>>)


RoleHasNoPolicyARN(w) ==
    \* role controller
    /\ workers[w].idle = FALSE
    /\ workers[w].req = rReq
    /\ role.policyArn = NULL
    /\ policy.awsPolicyArn # NULL
    /\ role' = [role EXCEPT !.policyArn = policy.awsPolicyArn]
    /\ Enqueue(<<rReq>>)
    /\ UNCHANGED <<awsPolicy, awsRole, awsPolicy, irsa, policy, sa, workers, wq>>


RoleHasPolicyARN(w) ==
    \* role controller
    /\ workers[w].idle = FALSE
    /\ workers[w].req = rReq
    /\ role.policyArn # NULL
    /\ role.roleArn # NULL
    /\ ~role.policiesAttached 
    /\ awsRole.attachedPolicy = NULL
    /\ awsRole' = [awsRole EXCEPT !.attachedPolicy = role.policyArn]
    /\ role' = [role EXCEPT !.policiesAttached = TRUE]
    /\ Enqueue(<<rReq>>)
    /\ UNCHANGED <<awsPolicy, irsa, policy, sa, workers, wq>>

CreateServiceAccount(w) ==
    \* irsa controller
    /\ workers[w].idle = FALSE
    /\ workers[w].req = iReq
    /\ sa.st = NULL
    /\ roleComplete
    /\ policyComplete
    /\ sa' = [sa EXCEPT !.st = "pending", !.name = irsa.saName, !.roleArn = role.roleArn ] 
    /\ Enqueue(<<saReq, iReq>>)
    /\ UNCHANGED <<awsPolicy, awsRole, irsa, policy, role, workers, wq>>


\* the following actions just "swallow" events when there's nothing to do on the resource
IrsaAllDone(w) ==
    /\ workers[w].idle = FALSE
    /\ workers[w].req = iReq
    /\ IrsaComplete
    /\ workers' = [workers EXCEPT ![w].idle = TRUE]
    /\ UNCHANGED <<awsPolicy, awsRole, irsa, policy, role, sa, wq, modified >>

PolicyAllDone(w) ==
    /\ workers[w].idle = FALSE
    /\ workers[w].req = pReq
    /\ policyComplete
    /\ workers' = [workers EXCEPT ![w].idle = TRUE]
    /\ UNCHANGED <<awsPolicy, awsRole, irsa, policy, role, sa, wq, modified>>

RoleAllDone(w) ==
    /\ workers[w].idle = FALSE
    /\ workers[w].req = rReq
    /\ roleComplete
    /\ workers' = [workers EXCEPT ![w].idle = TRUE]
    /\ UNCHANGED <<awsPolicy, awsRole, irsa, policy, role, sa, wq, modified>>

SaAllDone(w) ==
    /\ workers[w].idle = FALSE
    /\ workers[w].req = saReq
    /\ saComplete
    /\ workers' = [workers EXCEPT ![w].idle = TRUE]
    /\ UNCHANGED <<awsPolicy, awsRole, irsa, policy, role, sa, wq, modified>>


\* the whole state converged
Termination ==
    /\ \A w \in DOMAIN workers: workers[w].idle = TRUE /\ workers[w].req = NULL
    /\ IrsaComplete
    /\ roleComplete
    /\ policyComplete
    /\ saComplete
    /\ awsPolicy.arn # NULL
    /\ /\ awsRole.arn # NULL
       /\ awsRole.attachedPolicy # NULL
    /\ UNCHANGED vars


(***************************************************************************)
(* Spec                                                                    *)
(***************************************************************************)

Actions ==
    \/ Add
    \/ \E w \in _workers: \/ Get(w)
                          \/ Done(w)
                          \/ CreatePolicy(w)
                          \/ CreateRole(w)
                          \/ CreateServiceAccount(w)
                          \/ PolicyHasNoARN(w)
                          \/ RoleHasNoRoleARN(w)
                          \/ RoleHasNoPolicyARN(w)
                          \/ RoleHasPolicyARN(w)
                          \/ IrsaAllDone(w)
                          \/ PolicyAllDone(w)
                          \/ RoleAllDone(w)
                          \/ SaAllDone(w)


Fairness ==
    /\ WF_vars(Add)
    /\ WF_vars(Termination)
    /\ \A w \in _workers: /\ WF_vars(Get(w))
                          /\ WF_vars(Done(w))
                          /\ WF_vars(CreatePolicy(w))
                          /\ WF_vars(CreateRole(w))
                          /\ WF_vars(CreateServiceAccount(w))
                          /\ WF_vars(PolicyHasNoARN(w))
                          /\ WF_vars(RoleHasNoRoleARN(w))
                          /\ WF_vars(RoleHasNoPolicyARN(w))
                          /\ WF_vars(RoleHasPolicyARN(w))
                          /\ WF_vars(IrsaAllDone(w))
                          /\ WF_vars(PolicyAllDone(w))
                          /\ WF_vars(RoleAllDone(w))
                          /\ WF_vars(SaAllDone(w))


Next == 
    \/ Actions
    \/ Termination

Spec == 
    /\ Init
    /\ [][ Next ]_vars
    /\ []TypeOk
    /\ Fairness

(***************************************************************************)
(* Expectations                                                            *)
(***************************************************************************)

\* Safety
NoConcurrentProcessingOfSameResource == 
    [] \A w \in DOMAIN workers : \/ workers[w].idle 
                                 \/ workers[w].req \notin {workers[x].req: x \in DOMAIN workers \ {w}}

\* Liveness
TerminationIsTheLastAction == 
    [] ENABLED Termination ~> /\ ENABLED Termination
                              /\ ~ENABLED Actions
                              
THEOREM Spec => NoConcurrentProcessingOfSameResource
THEOREM Spec => TerminationIsTheLastAction

====
