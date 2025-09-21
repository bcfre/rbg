package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash"
	"hash/fnv"
	"sort"

	"github.com/davecgh/go-spew/spew"
	appsv1 "k8s.io/api/apps/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/strategicpatch"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/client"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
)

// ListRevisions lists all ControllerRevisions matching selector and owned by parent or no other
// controller. If the returned error is nil the returned slice of ControllerRevisions is valid. If the
// returned error is not nil, the returned slice is not valid.
func ListRevisions(
	ctx context.Context, k8sClient client.Client, parent metav1.Object, selector labels.Selector,
) ([]*appsv1.ControllerRevision, error) {
	// List all revisions in the namespace that match the selector
	revisionList := new(appsv1.ControllerRevisionList)
	err := k8sClient.List(
		ctx, revisionList, client.InNamespace(parent.GetNamespace()), client.MatchingLabelsSelector{Selector: selector},
	)
	if err != nil {
		return nil, err
	}
	history := revisionList.Items
	var owned []*appsv1.ControllerRevision
	for i := range history {
		ref := metav1.GetControllerOfNoCopy(&history[i])
		if ref == nil || ref.UID == parent.GetUID() {
			owned = append(owned, &history[i])
		}

	}
	return owned, err
}

func GetHighestRevision(revisions []*appsv1.ControllerRevision) *appsv1.ControllerRevision {
	count := len(revisions)
	if count <= 0 {
		return nil
	}

	max := int64(0)
	var maxRevision *appsv1.ControllerRevision
	for _, revision := range revisions {
		if max <= revision.Revision {
			max = revision.Revision
			maxRevision = revision
		}
	}
	return maxRevision
}

func EqualRevision(lhs, rhs *appsv1.ControllerRevision) bool {
	if lhs == nil || rhs == nil {
		return lhs == rhs
	}

	return bytes.Equal(lhs.Data.Raw, rhs.Data.Raw) && apiequality.Semantic.DeepEqual(lhs.Data.Object, rhs.Data.Object)
}

func ApplyRevision(rbg *workloadsv1alpha1.RoleBasedGroup, revision *appsv1.ControllerRevision) (*workloadsv1alpha1.RoleBasedGroup, error) {
	// clone := lws.DeepCopy()
	str := &bytes.Buffer{}
	err := unstructured.UnstructuredJSONScheme.Encode(rbg, str)
	if err != nil {
		return nil, err
	}
	patched, err := strategicpatch.StrategicMergePatch(str.Bytes(), revision.Data.Raw, rbg)
	if err != nil {
		return nil, err
	}
	restoredRbg := &workloadsv1alpha1.RoleBasedGroup{}
	if err = json.Unmarshal(patched, restoredRbg); err != nil {
		return nil, err
	}
	return restoredRbg, nil
}

func CleanExpiredRevision(ctx context.Context, client client.Client, rbg *workloadsv1alpha1.RoleBasedGroup, revisions []*appsv1.ControllerRevision) ([]*appsv1.ControllerRevision, error) {
	// todo: Use the default value temporarily, and add new attribute fields in RBG later
	exceedNum := len(revisions) - 10
	if exceedNum <= 0 {
		return revisions, nil
	}

	sort.SliceStable(revisions, func(i, j int) bool {
		if revisions[i].Revision == revisions[j].Revision {
			if revisions[i].CreationTimestamp.Equal(&revisions[j].CreationTimestamp) {
				return revisions[i].Name < revisions[j].Name
			}
			return revisions[i].CreationTimestamp.Before(&revisions[j].CreationTimestamp)
		}
		return revisions[i].Revision < revisions[j].Revision
	})

	for i, revision := range revisions {
		if i >= exceedNum {
			break
		}

		if err := client.Delete(context.TODO(), revision); err != nil {
			return revisions, err
		}
	}
	cleanedRevisions := revisions[exceedNum:]

	return cleanedRevisions, nil
}

func NewRevision(ctx context.Context, client client.Client, rbg *workloadsv1alpha1.RoleBasedGroup) (*appsv1.ControllerRevision, error) {
	selector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{MatchLabels: map[string]string{
		workloadsv1alpha1.SetNameLabelKey: rbg.Name,
	}})
	if err != nil {
		return nil, err
	}
	revisions, err := ListRevisions(ctx, client, rbg, selector)
	if err != nil {
		return nil, err
	}
	highestRevision := GetHighestRevision(revisions)
	revision := int64(1)
	if highestRevision != nil {
		revision = highestRevision.Revision + 1
	}
	rawPatch, err := getRBGPatch(rbg)
	if err != nil {
		return nil, err
	}

	cr := &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: rbg.Namespace,
			Labels: map[string]string{
				workloadsv1alpha1.SetNameLabelKey: rbg.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(rbg, workloadsv1alpha1.GroupVersion.WithKind(workloadsv1alpha1.RoleBasedGroupKind)),
			},
		},
		Data: runtime.RawExtension{
			Raw: rawPatch,
		},
		Revision: revision,
	}

	rgbHash, err := hashRevision(cr)
	if err != nil {
		return nil, err
	}
	cr.Labels[workloadsv1alpha1.RevisionKey] = rgbHash
	roleHashMap, err := getRoleHashMap(cr)
	if err != nil {
		return nil, err
	}
	for role, hash := range roleHashMap {
		cr.Labels[fmt.Sprintf(workloadsv1alpha1.RoleRevisionKeyFmt, role)] = hash
	}
	cr.Name = revisionName(rbg.Name, rgbHash, revision)
	return cr, nil
}

// revisionName returns the Name for a ControllerRevision in the form prefix-hash-revisionnumber. If the length
// of prefix is greater than 220 bytes, it is truncated to allow for a name that is no larger than 253 bytes.
// revision-number allows us to avoid collisions if the created prefix-hash already exists in the history, since revision
// will be unique.
func revisionName(prefix string, hash string, revisionNumber int64) string {
	if len(prefix) > 220 {
		prefix = prefix[:220]
	}

	return fmt.Sprintf("%s-%s-%v", prefix, hash, revisionNumber)
}

func getRoleHashMap(revision *appsv1.ControllerRevision) (map[string]string, error) {
	result := make(map[string]string)

	// ControllerRevision.Data.Raw 是 patch JSON 原始字节
	if len(revision.Data.Raw) == 0 {
		return result, nil
	}

	var obj map[string]interface{}
	if err := json.Unmarshal(revision.Data.Raw, &obj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ControllerRevision data: %w", err)
	}

	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("spec not found or wrong type")
	}

	roles, ok := spec["roles"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("roles not found or wrong type")
	}

	for _, r := range roles {
		roleMap, ok := r.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid role structure")
		}
		nameVal, ok := roleMap["name"].(string)
		if !ok || nameVal == "" {
			return nil, fmt.Errorf("role missing name field")
		}

		roleBytes, err := json.Marshal(roleMap)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal role: %w", err)
		}

		hf := fnv.New32a()
		if len(roleBytes) > 0 {
			hf.Write(roleBytes)
		}
		result[nameVal] = rand.SafeEncodeString(fmt.Sprint(hf.Sum32()))
	}

	return result, nil
}

func getRBGPatch(rbg *workloadsv1alpha1.RoleBasedGroup) ([]byte, error) {
	// str := &bytes.Buffer{}

	rbgBytes, err := json.Marshal(rbg)
	if err != nil {
		return nil, err
	}
	var raw map[string]interface{}
	err = json.Unmarshal(rbgBytes, &raw)
	if err != nil {
		return nil, err
	}

	objCopy := make(map[string]interface{})
	specCopy := make(map[string]interface{})
	spec := raw["spec"].(map[string]interface{})
	roles := spec["roles"].([]interface{})

	specCopy["roles"] = roles
	objCopy["spec"] = specCopy
	specCopy["$path"] = "replace"
	return json.Marshal(objCopy)
}

func hashRevision(revision *appsv1.ControllerRevision) (string, error) {
	hf := fnv.New32a()
	if len(revision.Data.Raw) > 0 {
		hf.Write(revision.Data.Raw)
	}
	if revision.Data.Object != nil {
		// hashutil.DeepHashObject(hf, revision.Data.Object)
		if err := deepHashObject(hf, revision.Data.Object); err != nil {
			return "", err
		}
	}
	return rand.SafeEncodeString(fmt.Sprint(hf.Sum32())), nil
}

func deepHashObject(hasher hash.Hash, objectToWrite interface{}) error {
	hasher.Reset()
	printer := spew.ConfigState{
		Indent:         " ",
		SortKeys:       true,
		DisableMethods: true,
		SpewKeys:       true,
	}
	_, err := printer.Fprintf(hasher, "%#v", objectToWrite)
	return err
}
