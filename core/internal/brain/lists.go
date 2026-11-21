package brain

import (
	"errors"
	"strings"
	"time"

	"cosmos/core/internal/store"
)

// listName settles what a list is called, so "my shopping list",
// "groceries" and "Shopping" are one list.
func listName(said string) string {
	n := norm(said)
	for _, lead := range []string{"my ", "the ", "our "} {
		n = strings.TrimPrefix(n, lead)
	}
	n = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(n, " lists"), " list"))
	switch n {
	case "", "list", "todo", "todos", "to do", "to dos", "task", "tasks", "things to do":
		return "to-do"
	case "shopping", "groceries", "grocery", "shop", "food shop", "supermarket":
		return "shopping"
	}
	return n
}

// sameItem reports whether two list lines are the same thing: "milk" and
// "Milk", or "the milk" and "milk".
func sameItem(a, b string) bool {
	x, y := norm(a), norm(b)
	if x == "" || y == "" {
		return false
	}
	return x == y || strings.Contains(" "+x+" ", " "+y+" ") || strings.Contains(" "+y+" ", " "+x+" ")
}

// saidBy reports whether every word of an item is something the user
// said. A list only ever holds their words.
func saidBy(item, said string) bool {
	have := " " + norm(said) + " "
	words := strings.Fields(norm(item))
	for _, w := range words {
		if !strings.Contains(have, " "+w+" ") && !strings.Contains(have, " "+w+"s ") && !strings.Contains(have, " "+strings.TrimSuffix(w, "s")+" ") {
			return false
		}
	}
	return len(words) > 0
}

// asksForList reports whether a message asks for something to be
// written on a list, or carries on from one that did ("oh and coffee
// too").
func asksForList(text string) bool {
	padded := " " + norm(text) + " "
	for _, w := range []string{" add ", " put ", " list ", " lists ", " note ", " write ", " jot ", " stick ", " pop "} {
		if strings.Contains(padded, w) {
			return true
		}
	}
	for _, lead := range []string{" and ", " oh and ", " also ", " plus ", " oh also "} {
		if strings.HasPrefix(padded, lead) {
			return true
		}
	}
	return strings.HasSuffix(padded, " too ") || strings.HasSuffix(padded, " as well ")
}

func openTexts(items []store.ListItem) []string {
	out := []string{}
	for _, it := range items {
		if !it.Done {
			out = append(out, it.Text)
		}
	}
	return out
}

// listAdd puts items on a list, leaving out any already on it and any
// the user never said.
func (b *Brain) listAdd(c StepCall, said string) (any, error) {
	name := listName(c.List)
	have, err := b.S.ListItems(name, b.Now())
	if err != nil {
		return nil, err
	}
	added, already := []string{}, []string{}
	for _, item := range c.Items {
		item = strings.TrimSpace(strings.Trim(strings.TrimSpace(item), ".,;"))
		if item == "" || !saidBy(item, said) {
			continue
		}
		dup := false
		for _, h := range have {
			dup = dup || (!h.Done && sameItem(h.Text, item))
		}
		if dup {
			already = append(already, item)
			continue
		}
		it, err := b.S.AddListItem(name, item, b.Now())
		if err != nil {
			return nil, err
		}
		have = append(have, it)
		added = append(added, item)
	}
	if len(added) == 0 && len(already) == 0 {
		return nil, errors.New("nothing was added: say which things should go on the " + name + " list")
	}
	b.Bus.Publish("lists", nil)
	out := map[string]any{"list": name, "added": added, "now_on_list": openTexts(have)}
	if len(already) > 0 {
		out["already_on_list"] = already
	}
	return out, nil
}

// listShow reads one list, or all of them when no list is named.
func (b *Brain) listShow(c StepCall) (any, error) {
	name := ""
	if strings.TrimSpace(c.List) != "" {
		name = listName(c.List)
	}
	items, err := b.S.ListItems(name, b.Now())
	if err != nil {
		return nil, err
	}
	if name != "" {
		return map[string]any{"list": name, "items": openTexts(items)}, nil
	}
	lists := map[string][]string{}
	for _, it := range items {
		if !it.Done {
			lists[it.List] = append(lists[it.List], it.Text)
		}
	}
	return map[string]any{"lists": lists}, nil
}

// listDone ticks items off. With no list named it looks on all of them.
func (b *Brain) listDone(c StepCall) (any, error) {
	name := ""
	if strings.TrimSpace(c.List) != "" {
		name = listName(c.List)
	}
	items, err := b.S.ListItems(name, b.Now())
	if err != nil {
		return nil, err
	}
	ticked, missing := []string{}, []string{}
	for _, want := range c.Items {
		found := false
		for i, it := range items {
			if !it.Done && sameItem(it.Text, want) {
				if err := b.S.SetListItemDone(it.ID, true, b.Now()); err != nil {
					return nil, err
				}
				items[i].Done = true
				ticked = append(ticked, it.Text)
				found = true
			}
		}
		if !found {
			missing = append(missing, want)
		}
	}
	if len(ticked) == 0 {
		return nil, errors.New("nothing was ticked off: " + strings.Join(missing, ", ") + " is not on the list")
	}
	b.Bus.Publish("lists", nil)
	out := map[string]any{"ticked_off": ticked, "still_on_list": openTexts(items)}
	if len(missing) > 0 {
		out["not_on_list"] = missing
	}
	return out, nil
}

// listRemove takes items off a list for good: they were a mistake, or
// are not wanted after all.
func (b *Brain) listRemove(c StepCall) (any, error) {
	name := ""
	if strings.TrimSpace(c.List) != "" {
		name = listName(c.List)
	}
	items, err := b.S.ListItems(name, b.Now())
	if err != nil {
		return nil, err
	}
	removed, missing := []string{}, []string{}
	gone := map[int64]bool{}
	for _, want := range c.Items {
		found := false
		for _, it := range items {
			if !it.Done && !gone[it.ID] && sameItem(it.Text, want) {
				if err := b.S.DeleteListItem(it.ID); err != nil {
					return nil, err
				}
				gone[it.ID] = true
				removed = append(removed, it.Text)
				found = true
			}
		}
		if !found {
			missing = append(missing, want)
		}
	}
	if len(removed) == 0 {
		return nil, errors.New("nothing was removed: " + strings.Join(missing, ", ") + " is not on the list")
	}
	left := []store.ListItem{}
	for _, it := range items {
		if !gone[it.ID] {
			left = append(left, it)
		}
	}
	b.Bus.Publish("lists", nil)
	out := map[string]any{"removed": removed, "still_on_list": openTexts(left)}
	if len(missing) > 0 {
		out["not_on_list"] = missing
	}
	return out, nil
}

// ListsView is every list for the app: open items, and those ticked off
// in the last day so a slip can be undone.
func (b *Brain) ListsView() ([]store.ListItem, error) {
	return b.S.ListItems("", b.Now().Add(-24*time.Hour))
}
