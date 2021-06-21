package v1alpha1

// poorman's golang enum
type CrCondition string

var (
	CrSubmitted   CrCondition = ""
	CrPending     CrCondition = "pending"
	CrProgressing CrCondition = "progressing"
	CrOK          CrCondition = "created"
	CrDeleting    CrCondition = "deleting"
	CrError       CrCondition = "error"
)

func (i CrCondition) String() string {
	return string(i)
}
