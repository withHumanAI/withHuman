package approvalpipeline

import (
	"context"
	"fmt"
	"slices"

	"github.com/withHumanAI/withHuman/domain"
)

// The effective pipeline is what one run executes: the entry pipeline's
// blocks with every delegate block immediately followed by the blocks of
// the pipeline it names, transitively, rebased into one pre-order index
// space. A delegate therefore reads exactly like a branch whose body is
// another pipeline: the cursor enters it or skips its subtree, and reports
// its end when the target runs out of blocks. Positions index the
// flattened list, so a suspension still records one integer, and every
// pipeline of the closure is pinned whether or not the request enters it,
// so a resume flattens the same revisions into the same positions.

// effectiveSection is one pinned revision in the effective pipeline: where
// its blocks sit and which delegate block inserted it.
type effectiveSection struct {
	Key      string
	Revision domain.RequestApprovalPipelineRevision
	Compiled *compiledPipeline
	// Enclosing indexes the section whose delegate block inserted this
	// one, -1 for the entry; DelegatePosition is that block's effective
	// position, -1 for the entry.
	Enclosing        int
	DelegatePosition int
	// Start and End bound the section's own blocks in effective positions,
	// End exclusive; a delegated pipeline's blocks sit inside the range.
	Start int
	End   int
}

// effectivePipeline is one run's flattened program. Sections lists the
// entry first, then every delegated pipeline in insertion order; Blocks
// and Snapshots share one pre-order index with Parent and End rebased to
// effective positions; Owner maps each position to its section.
type effectivePipeline struct {
	Sections  []effectiveSection
	Blocks    []compiledBlock
	Snapshots []domain.RequestApprovalBlock
	Owner     []int
}

// pinnedPipeline is one pipeline of the effective pipeline as the audit
// record names it.
type pinnedPipeline struct {
	Key      string
	ID       domain.ID
	Revision int64
	Name     string
}

// compiledResolver loads the compiled active or pinned revision of one
// pipeline key.
type compiledResolver func(ctx context.Context, key string) (*compiledPipeline, error)

// flattenEffective builds the effective pipeline from the root (the entry
// pipeline, or a previewed document) by resolving every delegate's target
// and splicing it in after the delegate block. It refuses a cycle, a
// delegation deeper than the limit, and a flattened list larger than the
// cap; a key is resolved once per run so a diamond pins one revision.
func flattenEffective(ctx context.Context, root *compiledPipeline, resolve compiledResolver) (*effectivePipeline, error) {
	effective := &effectivePipeline{}
	resolved := make(map[string]*compiledPipeline)
	memoised := func(ctx context.Context, key string) (*compiledPipeline, error) {
		if compiled, done := resolved[key]; done {
			return compiled, nil
		}
		compiled, err := resolve(ctx, key)
		if err != nil {
			return nil, err
		}
		resolved[key] = compiled
		return compiled, nil
	}
	if err := effective.appendSection(ctx, root, root.Revision.PipelineKey, -1, -1, 0, nil, memoised); err != nil {
		return nil, err
	}
	return effective, nil
}

func (effective *effectivePipeline) appendSection(ctx context.Context, section *compiledPipeline, key string, enclosing, delegatePosition, depth int, stack []string, resolve compiledResolver) error {
	if depth > domain.MaximumDelegationDepth {
		return domain.PipelineDelegationTooDeepError{Depth: depth}
	}
	if slices.Contains(stack, key) {
		return domain.PipelineDelegationCycleError{PipelineKeys: append(slices.Clone(stack), key)}
	}
	if len(effective.Blocks)+len(section.Blocks) > domain.MaximumEffectivePipelineBlocks {
		return fmt.Errorf("%w, the limit is %d", domain.ErrEffectivePipelineTooLarge, domain.MaximumEffectivePipelineBlocks)
	}
	if len(section.Blocks) != len(section.Snapshots) {
		return invalidPipelineDocument("compiled pipeline does not match its snapshots", nil)
	}
	sectionIndex := len(effective.Sections)
	effective.Sections = append(effective.Sections, effectiveSection{
		Key: key, Revision: section.Revision, Compiled: section,
		Enclosing: enclosing, DelegatePosition: delegatePosition, Start: len(effective.Blocks),
	})
	stack = append(stack, key)
	effectiveOf := make([]int, len(section.Blocks)+1)
	for index, block := range section.Blocks {
		effectiveOf[index] = len(effective.Blocks)
		effective.Blocks = append(effective.Blocks, block)
		effective.Snapshots = append(effective.Snapshots, section.Snapshots[index])
		effective.Owner = append(effective.Owner, sectionIndex)
		if !block.Delegate {
			continue
		}
		target, err := resolve(ctx, block.PipelineKey)
		if err != nil {
			return err
		}
		if err := effective.appendSection(ctx, target, block.PipelineKey, sectionIndex, effectiveOf[index], depth+1, stack, resolve); err != nil {
			return err
		}
	}
	effectiveOf[len(section.Blocks)] = len(effective.Blocks)
	effective.Sections[sectionIndex].End = len(effective.Blocks)
	// Rebase the tree: a block's parent and end move with it, a delegate's
	// end now covers the spliced target, and the target's top-level blocks
	// hang off the delegate exactly as a branch's blocks hang off it.
	for index, block := range section.Blocks {
		position := effectiveOf[index]
		if block.Parent >= 0 {
			effective.Blocks[position].Parent = effectiveOf[block.Parent]
		} else {
			effective.Blocks[position].Parent = delegatePosition
		}
		effective.Blocks[position].End = effectiveOf[block.End]
	}
	return nil
}

// entry is the revision every run starts in.
func (effective *effectivePipeline) entry() domain.RequestApprovalPipelineRevision {
	return effective.Sections[0].Revision
}

// keyAt is the pipeline key of the block at position.
func (effective *effectivePipeline) keyAt(position int) string {
	return effective.Sections[effective.Owner[position]].Key
}

// target is the section the delegate block at position inserted, -1 when
// the block is not a delegate. It scans sections rather than reading the
// next position's owner, because an entered empty target has no block of
// its own.
func (effective *effectivePipeline) target(position int) int {
	owner := effective.Owner[position]
	for index, section := range effective.Sections {
		if section.Enclosing == owner && section.DelegatePosition == position {
			return index
		}
	}
	return -1
}

// pinned maps every pipeline key of the run to the revision it selected.
func (effective *effectivePipeline) pinned() map[string]domain.ID {
	pins := make(map[string]domain.ID, len(effective.Sections))
	for _, section := range effective.Sections {
		pins[section.Key] = section.Revision.ID
	}
	return pins
}

// pins lists the run's pipelines for the audit record, the entry first
// and then every delegated pipeline once, in insertion order.
func (effective *effectivePipeline) pins() []pinnedPipeline {
	pins := make([]pinnedPipeline, 0, len(effective.Sections))
	seen := make(map[string]struct{}, len(effective.Sections))
	for _, section := range effective.Sections {
		if _, listed := seen[section.Key]; listed {
			continue
		}
		seen[section.Key] = struct{}{}
		pins = append(pins, pinnedPipeline{Key: section.Key, ID: section.Revision.ID, Revision: section.Revision.Revision, Name: section.Revision.Name})
	}
	return pins
}
