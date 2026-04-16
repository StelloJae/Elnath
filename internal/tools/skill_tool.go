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
	return "Create, list, or delete a wiki-native skill"
}

func (t *SkillTool) Schema() json.RawMessage {
	return Object(map[string]Property{
		"action":         StringEnum("Action to perform.", "create", "list", "delete"),
		"name":           String("Skill name (lowercase, hyphens)."),
		"description":    String("Short description of what the skill does."),
		"trigger":        String("Optional trigger text, e.g. /deploy-check <env>."),
		"required_tools": Array("Tools the skill requires.", "string"),
		"prompt":         String("Skill prompt with {arg} placeholders."),
	}, []string{"action"})
}

func (t *SkillTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *SkillTool) Reversible() bool { return true }

func (t *SkillTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *SkillTool) Scope(params json.RawMessage) ToolScope {
	var input skillToolInput
	if err := json.Unmarshal(params, &input); err != nil {
		return ConservativeScope()
	}
	wikiDir := t.wikiDir()
	if strings.TrimSpace(input.Action) == "list" {
		if wikiDir == "" {
			return ToolScope{}
		}
		return ToolScope{ReadPaths: []string{wikiDir}}
	}
	if wikiDir == "" {
		return ToolScope{Persistent: true}
	}
	return ToolScope{ReadPaths: []string{wikiDir}, WritePaths: []string{wikiDir}, Persistent: true}
}

type skillToolInput struct {
	Action        string   `json:"action"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Trigger       string   `json:"trigger"`
	RequiredTools []string `json:"required_tools"`
	Prompt        string   `json:"prompt"`
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
