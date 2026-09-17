package portfoliograph

import (
	"sort"
	"time"

	"gonum.org/v1/gonum/graph"
	"gonum.org/v1/gonum/graph/simple"
	"gonum.org/v1/gonum/graph/topo"

	"github.com/onixus/metis/internal/kernel"
)

// Graph — граф портфеля в памяти. Не потокобезопасен; синхронизацию обеспечивает Service.
type Graph struct {
	products     map[kernel.ID]*Product
	capabilities map[kernel.ID]*Capability
	features     map[kernel.ID]*Feature
	requirements map[kernel.ID]*Requirement
	links        map[kernel.ID]*Link
	contracts    map[kernel.ID]*IntegrationContract
	settings     Settings

	// Индексы зависимостей фич: out[f] — поставщики f (f зависит от них), in[f] — потребители f.
	out map[kernel.ID][]kernel.ID
	in  map[kernel.ID][]kernel.ID
	// crit[(from,to)] — максимальная критичность рёбер между парой фич.
	crit map[[2]kernel.ID]Criticality
}

// NewGraph создаёт пустой граф.
func NewGraph() *Graph {
	return &Graph{
		products:     map[kernel.ID]*Product{},
		capabilities: map[kernel.ID]*Capability{},
		features:     map[kernel.ID]*Feature{},
		requirements: map[kernel.ID]*Requirement{},
		links:        map[kernel.ID]*Link{},
		contracts:    map[kernel.ID]*IntegrationContract{},
		settings:     DefaultSettings(),
		out:          map[kernel.ID][]kernel.ID{},
		in:           map[kernel.ID][]kernel.ID{},
		crit:         map[[2]kernel.ID]Criticality{},
	}
}

func (g *Graph) putProduct(p Product)              { c := p; g.products[p.ID] = &c }
func (g *Graph) putCapability(c Capability)        { v := c; g.capabilities[c.ID] = &v }
func (g *Graph) putFeature(f Feature)              { v := f; g.features[f.ID] = &v }
func (g *Graph) putRequirement(r Requirement)      { v := r; g.requirements[r.ID] = &v }
func (g *Graph) putContract(c IntegrationContract) { v := c; g.contracts[c.ID] = &v }

func (g *Graph) putLink(l Link) {
	v := l
	g.links[l.ID] = &v
	if l.IsFeatureLevel() {
		key := [2]kernel.ID{l.FromFeatureID, l.ToFeatureID}
		if _, dup := g.crit[key]; !dup {
			g.out[l.FromFeatureID] = append(g.out[l.FromFeatureID], l.ToFeatureID)
			g.in[l.ToFeatureID] = append(g.in[l.ToFeatureID], l.FromFeatureID)
		}
		g.crit[key] = g.maxCrit(key)
	}
}

// maxCrit пересчитывает максимальную критичность по всем связям пары фич.
func (g *Graph) maxCrit(key [2]kernel.ID) Criticality {
	best := Criticality("")
	for _, l := range g.links {
		if l.FromFeatureID == key[0] && l.ToFeatureID == key[1] {
			if best == "" || g.settings.Coef(l.Criticality).GreaterThan(g.settings.Coef(best)) {
				best = l.Criticality
			}
		}
	}
	return best
}

func (g *Graph) removeLink(id kernel.ID) {
	l, ok := g.links[id]
	if !ok {
		return
	}
	delete(g.links, id)
	if l.IsFeatureLevel() {
		key := [2]kernel.ID{l.FromFeatureID, l.ToFeatureID}
		if c := g.maxCrit(key); c != "" {
			g.crit[key] = c
			return
		}
		delete(g.crit, key)
		g.out[l.FromFeatureID] = removeID(g.out[l.FromFeatureID], l.ToFeatureID)
		g.in[l.ToFeatureID] = removeID(g.in[l.ToFeatureID], l.FromFeatureID)
	}
}

func removeID(s []kernel.ID, id kernel.ID) []kernel.ID {
	for i, v := range s {
		if v == id {
			return append(s[:i:i], s[i+1:]...)
		}
	}
	return s
}

// FindCyclePath ищет путь from → … → to по текущим рёбрам зависимостей фич.
// Если такой путь есть, добавление ребра to → from замкнёт цикл (PG-05).
// Возвращает путь цикла с повторением первой вершины в конце или nil.
func (g *Graph) FindCyclePath(newFrom, newTo kernel.ID) []kernel.ID {
	if newFrom == newTo {
		return []kernel.ID{newFrom, newFrom}
	}
	// Ищем путь от newTo к newFrom по существующим рёбрам out (newTo зависит от … зависит от newFrom).
	parent := map[kernel.ID]kernel.ID{}
	visited := map[kernel.ID]bool{newTo: true}
	queue := []kernel.ID{newTo}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range g.out[cur] {
			if visited[next] {
				continue
			}
			visited[next] = true
			parent[next] = cur
			if next == newFrom {
				path := []kernel.ID{newFrom}
				for p := newFrom; p != newTo; {
					p = parent[p]
					path = append(path, p)
				}
				// path: newFrom → … → newTo; замыкаем новым ребром newFrom→newTo, разворачиваем в порядке зависимости.
				reverse(path)
				// теперь: newTo → … → newFrom; цикл: newFrom → newTo → … → newFrom
				cycle := append([]kernel.ID{newFrom}, path...)
				return cycle
			}
			queue = append(queue, next)
		}
	}
	return nil
}

func reverse(s []kernel.ID) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// IsAcyclic проверяет граф зависимостей фич целиком через gonum (инвариант модели).
func (g *Graph) IsAcyclic() bool {
	_, err := g.topoOrder()
	return err == nil
}

// topoOrder возвращает фичи в порядке «потребители раньше поставщиков»
// (для rollup ценности: сначала считаем зависимые, потом хаб).
func (g *Graph) topoOrder() ([]kernel.ID, error) {
	dg := simple.NewDirectedGraph()
	idOf := map[int64]kernel.ID{}
	nodeOf := map[kernel.ID]int64{}
	ids := make([]kernel.ID, 0, len(g.features))
	for id := range g.features {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	for i, id := range ids {
		n := int64(i)
		idOf[n] = id
		nodeOf[id] = n
		dg.AddNode(simple.Node(n))
	}
	for from, tos := range g.out {
		for _, to := range tos {
			nf, ok1 := nodeOf[from]
			nt, ok2 := nodeOf[to]
			if !ok1 || !ok2 || nf == nt {
				continue
			}
			dg.SetEdge(simple.Edge{F: simple.Node(nf), T: simple.Node(nt)})
		}
	}
	sorted, err := topo.Sort(dg)
	if err != nil {
		return nil, err
	}
	out := make([]kernel.ID, 0, len(sorted))
	for _, n := range sorted {
		out = append(out, idOf[n.ID()])
	}
	return out, nil
}

var _ graph.Directed = (*simple.DirectedGraph)(nil)

// Rollup считает производный спрос по формуле ТЗ 2.4 (PG-07):
// value(F) = own(F) + Σ value(fᵢ) × k(критичность), где fᵢ — фичи, зависящие от F.
// Фичи контракта наследуют его ценность с коэффициентом критичности контракта.
func (g *Graph) Rollup(now time.Time) ([]FeatureValue, error) {
	order, err := g.topoOrder()
	if err != nil {
		return nil, err
	}
	// Ценность контракта → собственная ценность его фич (обе стороны).
	contractBonus := map[kernel.ID]kernel.Money{}
	for _, c := range g.contracts {
		if c.SignalValue.IsZero() {
			continue
		}
		k := g.settings.Coef(c.Criticality)
		add := c.SignalValue.MulCoef(k)
		for _, fid := range append(append([]kernel.ID{}, c.ProviderFeatureIDs...), c.ConsumerFeatureIDs...) {
			sum, err := contractBonus[fid].Add(add)
			if err != nil {
				return nil, err
			}
			contractBonus[fid] = sum
		}
	}
	total := map[kernel.ID]kernel.Money{}
	values := make([]FeatureValue, 0, len(order))
	// topo.Sort даёт порядок «из → в»: потребители раньше поставщиков, что нам и нужно.
	for _, fid := range order {
		f := g.features[fid]
		own, err := f.OwnValue.Add(contractBonus[fid])
		if err != nil {
			return nil, err
		}
		derived := kernel.Money{Currency: own.Currency}
		for _, dep := range g.in[fid] {
			k := g.settings.Coef(g.linkCriticality(dep, fid))
			part := total[dep].MulCoef(k)
			derived, err = derived.Add(part)
			if err != nil {
				return nil, err
			}
		}
		sum, err := own.Add(derived)
		if err != nil {
			return nil, err
		}
		total[fid] = sum
		values = append(values, FeatureValue{
			FeatureID: fid, ProductID: f.ProductID, OwnValue: own, DerivedValue: derived, TotalValue: sum, ComputedAt: now,
		})
	}
	return values, nil
}

func (g *Graph) linkCriticality(from, to kernel.ID) Criticality {
	return g.crit[[2]kernel.ID{from, to}]
}

// PropagateShift находит фичи, затронутые сдвигом даты поставщика (PG-08).
// Затронуты все транзитивные потребители; ImpliedDate = max(своя дата, новая дата поставщика).
func (g *Graph) PropagateShift(source kernel.ID, newDate kernel.Date) []AffectedFeature {
	type item struct {
		id    kernel.ID
		via   kernel.ID
		depth int
	}
	seen := map[kernel.ID]bool{source: true}
	queue := []item{}
	for _, c := range g.in[source] {
		queue = append(queue, item{id: c, via: source, depth: 1})
	}
	var out []AffectedFeature
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur.id] {
			continue
		}
		seen[cur.id] = true
		f := g.features[cur.id]
		if f == nil {
			continue
		}
		implied := kernel.MaxDate(f.PlannedDate, newDate)
		out = append(out, AffectedFeature{
			FeatureID: f.ID, ProductID: f.ProductID, PlannedDate: f.PlannedDate, ImpliedDate: implied, ViaFeature: cur.via, Depth: cur.depth,
		})
		for _, c := range g.in[cur.id] {
			queue = append(queue, item{id: c, via: cur.id, depth: cur.depth + 1})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Depth != out[j].Depth {
			return out[i].Depth < out[j].Depth
		}
		return out[i].FeatureID.String() < out[j].FeatureID.String()
	})
	return out
}

// ContractReadyDate — срок готовности контракта: максимум сроков его фич на обеих сторонах (ТЗ 2.4).
func (g *Graph) ContractReadyDate(c *IntegrationContract) kernel.Date {
	dates := make([]kernel.Date, 0, len(c.ProviderFeatureIDs)+len(c.ConsumerFeatureIDs))
	for _, fid := range append(append([]kernel.ID{}, c.ProviderFeatureIDs...), c.ConsumerFeatureIDs...) {
		if f := g.features[fid]; f != nil {
			dates = append(dates, kernel.MaxDate(f.PlannedDate, f.ImpliedDate))
		}
	}
	return kernel.MaxDate(dates...)
}

// Hubs считает роль хаба по входящей связности (PG-06): для каждого продукта — число
// продуктов, зависящих от него по связям уровня продуктов. Computed — у максимума (при ненулевой связности).
func (g *Graph) Hubs() []HubInfo {
	in := map[kernel.ID]map[kernel.ID]bool{}
	for _, l := range g.links {
		if l.FromProductID == l.ToProductID {
			continue
		}
		if in[l.ToProductID] == nil {
			in[l.ToProductID] = map[kernel.ID]bool{}
		}
		in[l.ToProductID][l.FromProductID] = true
	}
	out := make([]HubInfo, 0, len(g.products))
	maxDeg := 0
	for id, p := range g.products {
		deg := len(in[id])
		if deg > maxDeg {
			maxDeg = deg
		}
		out = append(out, HubInfo{ProductID: id, InDegree: deg, Manual: p.HubManual})
	}
	for i := range out {
		out[i].Computed = maxDeg > 0 && out[i].InDegree == maxDeg
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].InDegree != out[j].InDegree {
			return out[i].InDegree > out[j].InDegree
		}
		return out[i].ProductID.String() < out[j].ProductID.String()
	})
	return out
}

// LinkedProducts — продукты, связанные с данным ребром любого типа (для PG-10 / identityaccess).
func (g *Graph) LinkedProducts(id kernel.ID) []kernel.ID {
	set := map[kernel.ID]bool{}
	for _, l := range g.links {
		switch {
		case l.FromProductID == id && l.ToProductID != id:
			set[l.ToProductID] = true
		case l.ToProductID == id && l.FromProductID != id:
			set[l.FromProductID] = true
		}
	}
	out := make([]kernel.ID, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}
