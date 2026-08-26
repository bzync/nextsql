package page

import (
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

func (p *Page) Validate() error {
	if p == nil || len(p.buf) != format.LogicalPageSize {
		return nerr.New(nerr.InvalidFormat, "page.Validate", "logical page has wrong size")
	}
	if !format.HasMagic(p.buf, offMagic) {
		return nerr.New(nerr.InvalidFormat, "page.Validate", "bad page magic")
	}
	ver := encoding.U16(p.buf, offVersion)
	if ver != format.CurrentFormatVersion {
		return nerr.New(nerr.InvalidFormat, "page.Validate", "unsupported page format version")
	}
	typ := format.PageType(encoding.U16(p.buf, offType))
	if !typ.Known() {
		return nerr.New(nerr.InvalidFormat, "page.Validate", "unknown page type")
	}
	slotCount := int(encoding.U16(p.buf, offSlotCount))
	lower := int(encoding.U16(p.buf, offLower))
	upper := int(encoding.U16(p.buf, offUpper))
	if slotCount < 0 || slotCount > (format.LogicalPageSize-format.PageHeaderSize)/format.SlotSize {
		return nerr.New(nerr.Corruption, "page.Validate", "invalid slot count")
	}
	wantLower := format.PageHeaderSize + slotCount*format.SlotSize
	if lower != wantLower {
		return nerr.New(nerr.Corruption, "page.Validate", "lower pointer does not match slot directory")
	}
	if upper < lower || upper > format.LogicalPageSize {
		return nerr.New(nerr.Corruption, "page.Validate", "invalid upper pointer")
	}

	type interval struct{ lo, hi int }
	live := make([]interval, 0, slotCount)
	for i := 0; i < slotCount; i++ {
		off, length := p.slot(i)
		if length == 0 && off == 0 {
			continue
		}
		start := int(off)
		end := start + int(length)
		if start < upper || end > format.LogicalPageSize || start > end {
			return nerr.New(nerr.Corruption, "page.Validate", "slot outside record area")
		}
		live = append(live, interval{start, end})
	}
	if len(live) > 1 {
		// Sort by start so overlap is a linear scan, not O(n²).
		for i := 1; i < len(live); i++ {
			j := i
			for j > 0 && live[j].lo < live[j-1].lo {
				live[j], live[j-1] = live[j-1], live[j]
				j--
			}
		}
		for i := 1; i < len(live); i++ {
			if live[i].lo < live[i-1].hi && live[i-1].lo < live[i].hi {
				return nerr.New(nerr.Corruption, "page.Validate", "overlapping slots")
			}
		}
	}
	return nil
}
