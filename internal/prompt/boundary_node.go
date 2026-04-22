package prompt

import "context"

const dynamicBoundary = "__DYNAMIC_BOUNDARY__"

type DynamicBoundaryNode struct{}

func NewDynamicBoundaryNode() *DynamicBoundaryNode {
	return &DynamicBoundaryNode{}
}

func (n *DynamicBoundaryNode) Name() string {
	return "dynamic_boundary"
}

func (n *DynamicBoundaryNode) Priority() int {
	return 999
}

func (n *DynamicBoundaryNode) Render(_ context.Context, _ *RenderState) (string, error) {
	return dynamicBoundary, nil
}

// CacheBoundary classifies the boundary marker as stable: it terminates
// the cacheable prefix and itself carries no per-session content.
func (n *DynamicBoundaryNode) CacheBoundary() CacheBoundary {
	return CacheBoundaryStable
}
