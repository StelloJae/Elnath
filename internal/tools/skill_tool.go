package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"unsafe"
)

type SkillTool struct {
	creator  any
	registry any
	deleted  map[string]struct{}
}

func NewSkillTool(creator any, registry any) *SkillTool {
	return &SkillTool{creator: creator, registry: registry, deleted: make(map[string]struct{})}
}

func (t *SkillTool) Name() string { return "create_skill" }

func (t *SkillTool) Description() string {
	return "Create, list, delete, propose, or apply a safe improvement for a wiki-native skill"
}

func (t *SkillTool) Schema() json.RawMessage {
	return Object(map[string]Property{
		"action":         StringEnum("Action to perform.", "create", "list", "delete", "propose_improvement", "apply_improvement"),
		"name":           String("Skill name (lowercase, hyphens)."),
		"description":    String("Short description of what the skill does."),
		"trigger":        String("Optional trigger text, e.g. /deploy-check <env>."),
		"required_tools": Array("Tools the skill requires.", "string"),
		"prompt":         String("Skill prompt with {arg} placeholders."),
		"session_id":     String("Optional session ID tied to a skill improvement proposal."),
		"reason":         String("Reason for action=propose_improvement."),
		"evidence":       Array("Evidence lines for action=propose_improvement.", "string"),
		"suggested_change": String(
			"Suggested skill change for action=propose_improvement. Writes a review artifact, not the skill file.",
		),
		"proposal_path": String("Proposal artifact path for action=apply_improvement."),
		"approved":      Bool("Must be true for action=apply_improvement."),
	}, []string{"action"})
}

func (t *SkillTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *SkillTool) Reversible() bool { return true }

func (t *SkillTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *SkillTool) DeferInitialToolSchema() bool { return true }

func (t *SkillTool) Scope(params json.RawMessage) ToolScope {
	var input skillToolInput
	if err := json.Unmarshal(params, &input); err != nil {
		return ConservativeScope()
	}
	wikiDir := t.wikiDir()
	action := strings.TrimSpace(input.Action)
	if action == "list" {
		if wikiDir == "" {
			return ToolScope{}
		}
		return ToolScope{ReadPaths: []string{wikiDir}}
	}
	if action == "propose_improvement" {
		proposalDir := t.proposalDir()
		if proposalDir == "" {
			return ToolScope{Persistent: true}
		}
		return ToolScope{WritePaths: []string{proposalDir}, Persistent: true}
	}
	if action == "apply_improvement" {
		scope := ToolScope{Persistent: true}
		if proposalPath := strings.TrimSpace(input.ProposalPath); proposalPath != "" {
			scope.ReadPaths = append(scope.ReadPaths, filepath.Clean(proposalPath))
		}
		if wikiDir != "" {
			scope.ReadPaths = append(scope.ReadPaths, wikiDir)
			scope.WritePaths = append(scope.WritePaths, wikiDir)
		}
		return scope
	}
	if wikiDir == "" {
		return ToolScope{Persistent: true}
	}
	return ToolScope{ReadPaths: []string{wikiDir}, WritePaths: []string{wikiDir}, Persistent: true}
}

type skillToolInput struct {
	Action          string   `json:"action"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Trigger         string   `json:"trigger"`
	RequiredTools   []string `json:"required_tools"`
	Prompt          string   `json:"prompt"`
	SessionID       string   `json:"session_id"`
	Reason          string   `json:"reason"`
	Evidence        []string `json:"evidence"`
	SuggestedChange string   `json:"suggested_change"`
	ProposalPath    string   `json:"proposal_path"`
	Approved        bool     `json:"approved"`
}

func (t *SkillTool) Execute(_ context.Context, params json.RawMessage) (*Result, error) {
	var input skillToolInput
	if err := json.Unmarshal(params, &input); err != nil {
		return ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
	}

	switch strings.TrimSpace(input.Action) {
	case "create":
		return t.executeCreate(input)
	case "list":
		return t.executeList(), nil
	case "delete":
		return t.executeDelete(input)
	case "propose_improvement":
		return t.executeProposeImprovement(input)
	case "apply_improvement":
		return t.executeApplyImprovement(input)
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %q", input.Action)), nil
	}
}

func (t *SkillTool) executeCreate(input skillToolInput) (*Result, error) {
	if strings.TrimSpace(input.Name) == "" {
		return ErrorResult("name must not be empty"), nil
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return ErrorResult("prompt must not be empty"), nil
	}
	if t == nil || t.creator == nil {
		return ErrorResult("skill creator is not configured"), nil
	}

	sk, err := t.createSkill(input)
	if err != nil {
		return ErrorResult(fmt.Sprintf("create_skill: %v", err)), nil
	}
	if t.registry != nil {
		t.addSkillToRegistry(sk)
	}
	delete(t.deleted, strings.TrimSpace(input.Name))

	name := firstNonEmpty(reflectStringField(sk, "Name"), strings.TrimSpace(input.Name))
	desc := reflectStringField(sk, "Description")
	message := fmt.Sprintf("Created skill /%s", name)
	if desc != "" {
		message += " - " + desc
	}
	return SuccessResult(message), nil
}

func (t *SkillTool) executeList() *Result {
	if t == nil || t.registry == nil {
		return SuccessResult("No skills registered.")
	}

	skills, err := t.listSkills()
	if err != nil {
		return ErrorResult(fmt.Sprintf("create_skill: %v", err))
	}
	if len(skills) == 0 {
		return SuccessResult("No skills registered.")
	}

	var b strings.Builder
	b.WriteString("Registered skills:\n")
	count := 0
	for _, sk := range skills {
		name := reflectStringField(sk, "Name")
		if _, deleted := t.deleted[name]; deleted {
			continue
		}
		count++
		desc := reflectStringField(sk, "Description")
		fmt.Fprintf(&b, "- /%s", name)
		if desc != "" {
			b.WriteString(" - ")
			b.WriteString(desc)
		}
		b.WriteByte('\n')
	}
	if count == 0 {
		return SuccessResult("No skills registered.")
	}
	return SuccessResult(strings.TrimRight(b.String(), "\n"))
}

func (t *SkillTool) executeDelete(input skillToolInput) (*Result, error) {
	if strings.TrimSpace(input.Name) == "" {
		return ErrorResult("name must not be empty"), nil
	}
	if t == nil || t.creator == nil {
		return ErrorResult("skill creator is not configured"), nil
	}
	if err := t.deleteSkill(input.Name); err != nil {
		return ErrorResult(fmt.Sprintf("create_skill: %v", err)), nil
	}
	t.removeSkillFromRegistry(strings.TrimSpace(input.Name))
	if t.deleted == nil {
		t.deleted = make(map[string]struct{})
	}
	t.deleted[strings.TrimSpace(input.Name)] = struct{}{}
	return SuccessResult(fmt.Sprintf("Deleted skill /%s", strings.TrimSpace(input.Name))), nil
}

func (t *SkillTool) executeProposeImprovement(input skillToolInput) (*Result, error) {
	if strings.TrimSpace(input.Name) == "" {
		return ErrorResult("name must not be empty"), nil
	}
	if strings.TrimSpace(input.Reason) == "" {
		return ErrorResult("reason must not be empty"), nil
	}
	if strings.TrimSpace(input.SuggestedChange) == "" {
		return ErrorResult("suggested_change must not be empty"), nil
	}
	if t == nil || t.creator == nil {
		return ErrorResult("skill creator is not configured"), nil
	}
	path, err := t.proposeSkillImprovement(input)
	if err != nil {
		return ErrorResult(fmt.Sprintf("create_skill: %v", err)), nil
	}
	return SuccessResult(fmt.Sprintf("Proposed improvement for /%s: %s", strings.TrimSpace(input.Name), path)), nil
}

func (t *SkillTool) executeApplyImprovement(input skillToolInput) (*Result, error) {
	if !input.Approved {
		return ErrorResult("approved must be true to apply a skill improvement proposal"), nil
	}
	if strings.TrimSpace(input.ProposalPath) == "" {
		return ErrorResult("proposal_path must not be empty"), nil
	}
	if t == nil || t.creator == nil {
		return ErrorResult("skill creator is not configured"), nil
	}
	name, err := t.applySkillImprovement(input)
	if err != nil {
		return ErrorResult(fmt.Sprintf("create_skill: %v", err)), nil
	}
	return SuccessResult(fmt.Sprintf("Applied improvement to /%s", name)), nil
}

func (t *SkillTool) applySkillImprovement(input skillToolInput) (string, error) {
	method := reflect.ValueOf(t.creator).MethodByName("ApplyImprovementProposal")
	if !method.IsValid() {
		return "", fmt.Errorf("skill creator does not implement ApplyImprovementProposal")
	}
	if method.Type().NumIn() != 1 || method.Type().NumOut() != 2 {
		return "", fmt.Errorf("skill creator ApplyImprovementProposal signature mismatch")
	}
	results := method.Call([]reflect.Value{reflect.ValueOf(strings.TrimSpace(input.ProposalPath))})
	if err := reflectedError(results[1]); err != nil {
		return "", err
	}
	name := reflectStringField(results[0], "Name")
	if name == "" {
		return "", fmt.Errorf("skill creator ApplyImprovementProposal returned invalid skill")
	}
	return name, nil
}

func (t *SkillTool) proposeSkillImprovement(input skillToolInput) (string, error) {
	method := reflect.ValueOf(t.creator).MethodByName("ProposeImprovement")
	if !method.IsValid() {
		return "", fmt.Errorf("skill creator does not implement ProposeImprovement")
	}
	if method.Type().NumIn() != 1 || method.Type().NumOut() != 2 || method.Type().Out(0).Kind() != reflect.String {
		return "", fmt.Errorf("skill creator ProposeImprovement signature mismatch")
	}
	params := reflect.New(method.Type().In(0)).Elem()
	setStructField(params, "SkillName", strings.TrimSpace(input.Name))
	setStructField(params, "SessionID", strings.TrimSpace(input.SessionID))
	setStructField(params, "Reason", strings.TrimSpace(input.Reason))
	setStructField(params, "Evidence", append([]string(nil), input.Evidence...))
	setStructField(params, "SuggestedChange", strings.TrimSpace(input.SuggestedChange))
	results := method.Call([]reflect.Value{params})
	if err := reflectedError(results[1]); err != nil {
		return "", err
	}
	return results[0].String(), nil
}

func (t *SkillTool) createSkill(input skillToolInput) (reflect.Value, error) {
	method := reflect.ValueOf(t.creator).MethodByName("Create")
	if !method.IsValid() {
		return reflect.Value{}, fmt.Errorf("skill creator does not implement Create")
	}
	if method.Type().NumIn() != 1 || method.Type().NumOut() != 2 {
		return reflect.Value{}, fmt.Errorf("skill creator Create signature mismatch")
	}

	params := reflect.New(method.Type().In(0)).Elem()
	setStructField(params, "Name", strings.TrimSpace(input.Name))
	setStructField(params, "Description", strings.TrimSpace(input.Description))
	setStructField(params, "Trigger", strings.TrimSpace(input.Trigger))
	setStructField(params, "RequiredTools", append([]string(nil), input.RequiredTools...))
	setStructField(params, "Prompt", input.Prompt)
	setStructField(params, "Status", "active")
	setStructField(params, "Source", "hint")

	results := method.Call([]reflect.Value{params})
	if err := reflectedError(results[1]); err != nil {
		return reflect.Value{}, err
	}
	return results[0], nil
}

func (t *SkillTool) deleteSkill(name string) error {
	method := reflect.ValueOf(t.creator).MethodByName("Delete")
	if !method.IsValid() {
		return fmt.Errorf("skill creator does not implement Delete")
	}
	if method.Type().NumIn() != 1 || method.Type().NumOut() != 1 {
		return fmt.Errorf("skill creator Delete signature mismatch")
	}
	results := method.Call([]reflect.Value{reflect.ValueOf(strings.TrimSpace(name))})
	return reflectedError(results[0])
}

func (t *SkillTool) listSkills() ([]reflect.Value, error) {
	method := reflect.ValueOf(t.registry).MethodByName("List")
	if !method.IsValid() {
		return nil, fmt.Errorf("skill registry does not implement List")
	}
	if method.Type().NumIn() != 0 || method.Type().NumOut() != 1 {
		return nil, fmt.Errorf("skill registry List signature mismatch")
	}
	results := method.Call(nil)
	list := indirectValue(results[0])
	if !list.IsValid() || list.Kind() != reflect.Slice {
		return nil, fmt.Errorf("skill registry List returned non-slice")
	}

	out := make([]reflect.Value, 0, list.Len())
	for i := 0; i < list.Len(); i++ {
		out = append(out, list.Index(i))
	}
	return out, nil
}

func (t *SkillTool) addSkillToRegistry(skillValue reflect.Value) {
	method := reflect.ValueOf(t.registry).MethodByName("Add")
	if !method.IsValid() {
		return
	}
	if method.Type().NumIn() != 1 || method.Type().NumOut() != 0 {
		return
	}
	skillValue = indirectValue(skillValue)
	if !skillValue.IsValid() {
		return
	}
	if skillValue.Kind() != reflect.Pointer {
		if skillValue.CanAddr() {
			skillValue = skillValue.Addr()
		} else {
			return
		}
	}
	method.Call([]reflect.Value{skillValue})
}

func (t *SkillTool) wikiDir() string {
	if t == nil || t.creator == nil {
		return ""
	}
	creator := indirectValue(reflect.ValueOf(t.creator))
	if !creator.IsValid() || creator.Kind() != reflect.Struct {
		return ""
	}
	store := unsafeField(creator.FieldByName("store"))
	if !store.IsValid() {
		return ""
	}
	if store.Kind() == reflect.Pointer && store.IsNil() {
		return ""
	}
	if store.CanInterface() {
		store = reflect.ValueOf(store.Interface())
	}
	method := store.MethodByName("WikiDir")
	if !method.IsValid() && store.CanAddr() {
		method = store.Addr().MethodByName("WikiDir")
	}
	if !method.IsValid() {
		return ""
	}
	if method.Type().NumIn() != 0 || method.Type().NumOut() != 1 || method.Type().Out(0).Kind() != reflect.String {
		return ""
	}
	results := method.Call(nil)
	return filepath.Clean(strings.TrimSpace(results[0].String()))
}

func (t *SkillTool) proposalDir() string {
	if t == nil || t.creator == nil {
		return ""
	}
	creator := indirectValue(reflect.ValueOf(t.creator))
	if !creator.IsValid() || creator.Kind() != reflect.Struct {
		return ""
	}
	tracker := unsafeField(creator.FieldByName("tracker"))
	if !tracker.IsValid() {
		return ""
	}
	tracker = indirectValue(tracker)
	if !tracker.IsValid() || tracker.Kind() != reflect.Struct {
		return ""
	}
	proposalDir := unsafeField(tracker.FieldByName("proposalDir"))
	if !proposalDir.IsValid() || proposalDir.Kind() != reflect.String {
		return ""
	}
	return filepath.Clean(strings.TrimSpace(proposalDir.String()))
}

func (t *SkillTool) removeSkillFromRegistry(name string) {
	if t == nil || t.registry == nil || name == "" {
		return
	}
	registry := indirectValue(reflect.ValueOf(t.registry))
	if !registry.IsValid() || registry.Kind() != reflect.Struct {
		return
	}
	skills := unsafeField(registry.FieldByName("skills"))
	if !skills.IsValid() || skills.Kind() != reflect.Map || skills.IsNil() {
		return
	}
	skills.SetMapIndex(reflect.ValueOf(name), reflect.Value{})
}

func setStructField(target reflect.Value, name string, value any) {
	field := target.FieldByName(name)
	if !field.IsValid() || !field.CanSet() {
		return
	}
	val := reflect.ValueOf(value)
	if !val.IsValid() {
		return
	}
	if val.Type().AssignableTo(field.Type()) {
		field.Set(val)
		return
	}
	if val.Type().ConvertibleTo(field.Type()) {
		field.Set(val.Convert(field.Type()))
	}
}

func reflectStringField(value reflect.Value, fieldName string) string {
	value = indirectValue(value)
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return ""
	}
	field := value.FieldByName(fieldName)
	if !field.IsValid() || field.Kind() != reflect.String {
		return ""
	}
	return strings.TrimSpace(field.String())
}

func indirectValue(value reflect.Value) reflect.Value {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return reflect.Value{}
		}
		value = value.Elem()
	}
	return value
}

func unsafeField(value reflect.Value) reflect.Value {
	if !value.IsValid() {
		return value
	}
	if value.CanAddr() {
		return reflect.NewAt(value.Type(), unsafe.Pointer(value.UnsafeAddr())).Elem()
	}
	return value
}

func reflectedError(value reflect.Value) error {
	if !value.IsValid() || value.IsNil() {
		return nil
	}
	err, _ := value.Interface().(error)
	return err
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
