package main

import (
	"fmt"
	"strconv"
	"strings"
)

// ---- statement structures ----

type selItem struct {
	expr  sx
	alias string
	star  bool
}

type sqlJoin struct {
	kind string // klog join kind
	src  string // file path
	on   sx     // ON condition (equalities)
}

type orderItem struct {
	expr sx
	pos  int // 1-based positional reference, or 0
	desc bool
}

type selectStmt struct {
	distinct bool
	items    []selItem
	from     string
	joins    []sqlJoin
	where    sx
	groupBy  []sx
	having   sx
	orderBy  []orderItem
	limit    int
	hasLimit bool
}

// translateSQL parses a SQL statement and returns an equivalent klog pipeline
// plus an optional input file named in FROM (used when no file args are given).
func translateSQL(sql string) (pipeline string, fromFile string, err error) {
	toks, err := sqlLex(sql)
	if err != nil {
		return "", "", err
	}
	p := &sqlParser{toks: toks}
	stmt, err := p.parseSelect()
	if err != nil {
		return "", "", err
	}
	if p.peek().kind != sqlEOF {
		return "", "", fmt.Errorf("unexpected token %q after statement", p.peek().val)
	}
	pipe, err := stmt.toPipeline()
	if err != nil {
		return "", "", err
	}
	return pipe, stmt.fromFile(), nil
}

func (s *selectStmt) fromFile() string {
	f := s.from
	if f == "" {
		return ""
	}
	if strings.ContainsAny(f, "/.") {
		return f
	}
	return ""
}

// ---- statement parser ----

var clauseStop = map[string]bool{
	"FROM": true, "WHERE": true, "GROUP": true, "HAVING": true,
	"ORDER": true, "LIMIT": true, "OFFSET": true,
}

func (p *sqlParser) parseSelect() (*selectStmt, error) {
	if !p.isKw("SELECT") {
		return nil, fmt.Errorf("expected SELECT")
	}
	p.next()
	st := &selectStmt{}
	if p.isKw("DISTINCT") {
		st.distinct = true
		p.next()
	}
	// select items
	for {
		if p.peek().kind == sqlStar {
			p.next()
			st.items = append(st.items, selItem{star: true})
		} else {
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			it := selItem{expr: e}
			if p.isKw("AS") {
				p.next()
				it.alias = p.next().val
			} else if p.peek().kind == sqlIdent {
				it.alias = p.next().val
			}
			st.items = append(st.items, it)
		}
		if p.peek().kind == sqlComma {
			p.next()
			continue
		}
		break
	}
	// FROM
	if p.isKw("FROM") {
		p.next()
		src := p.next()
		if src.kind != sqlIdent && src.kind != sqlStr {
			return nil, fmt.Errorf("FROM expects a table or file name")
		}
		st.from = src.val
		// optional alias
		if p.peek().kind == sqlIdent {
			p.next()
		}
		// joins
		for {
			j, ok, err := p.parseJoin()
			if err != nil {
				return nil, err
			}
			if !ok {
				break
			}
			st.joins = append(st.joins, j)
		}
	}
	if p.isKw("WHERE") {
		p.next()
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		st.where = e
	}
	if p.isKw("GROUP") {
		p.next()
		if !p.isKw("BY") {
			return nil, fmt.Errorf("GROUP expects BY")
		}
		p.next()
		for {
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			st.groupBy = append(st.groupBy, e)
			if p.peek().kind == sqlComma {
				p.next()
				continue
			}
			break
		}
	}
	if p.isKw("HAVING") {
		p.next()
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		st.having = e
	}
	if p.isKw("ORDER") {
		p.next()
		if !p.isKw("BY") {
			return nil, fmt.Errorf("ORDER expects BY")
		}
		p.next()
		for {
			oi := orderItem{}
			if p.peek().kind == sqlNum {
				if n, err := strconv.Atoi(p.peek().val); err == nil {
					oi.pos = n
					p.next()
				}
			}
			if oi.pos == 0 {
				e, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				oi.expr = e
			}
			if p.isKw("ASC") {
				p.next()
			} else if p.isKw("DESC") {
				oi.desc = true
				p.next()
			}
			st.orderBy = append(st.orderBy, oi)
			if p.peek().kind == sqlComma {
				p.next()
				continue
			}
			break
		}
	}
	if p.isKw("LIMIT") {
		p.next()
		n := p.next()
		if n.kind != sqlNum {
			return nil, fmt.Errorf("LIMIT expects a number")
		}
		v, _ := strconv.Atoi(n.val)
		st.limit = v
		st.hasLimit = true
		if p.isKw("OFFSET") {
			p.next()
			p.next()
		}
	}
	return st, nil
}

func (p *sqlParser) parseJoin() (sqlJoin, bool, error) {
	kind := "inner"
	start := p.pos
	switch {
	case p.isKw("INNER"):
		p.next()
	case p.isKw("LEFT"):
		p.next()
		kind = "leftouter"
		if p.isKw("OUTER") {
			p.next()
		}
	case p.isKw("RIGHT"):
		p.next()
		kind = "rightouter"
		if p.isKw("OUTER") {
			p.next()
		}
	case p.isKw("FULL"):
		p.next()
		kind = "fullouter"
		if p.isKw("OUTER") {
			p.next()
		}
	case p.isKw("CROSS"):
		p.next()
	}
	if !p.isKw("JOIN") {
		p.pos = start
		return sqlJoin{}, false, nil
	}
	p.next()
	src := p.next()
	if src.kind != sqlIdent && src.kind != sqlStr {
		return sqlJoin{}, false, fmt.Errorf("JOIN expects a file name")
	}
	// optional alias
	if p.peek().kind == sqlIdent {
		p.next()
	}
	if !p.isKw("ON") {
		return sqlJoin{}, false, fmt.Errorf("JOIN expects ON")
	}
	p.next()
	on, err := p.parseExpr()
	if err != nil {
		return sqlJoin{}, false, err
	}
	return sqlJoin{kind: kind, src: src.val, on: on}, true, nil
}

// ---- translation ----

func (s *selectStmt) toPipeline() (string, error) {
	var stages []string

	for _, j := range s.joins {
		keys, err := onToKeys(j.on)
		if err != nil {
			return "", err
		}
		stages = append(stages, fmt.Sprintf("join kind=%s (%s) on %s", j.kind, j.src, keys))
	}
	if s.where != nil {
		stages = append(stages, "where "+s.where.kql())
	}

	grouped := len(s.groupBy) > 0 || s.hasAggregate()
	if grouped {
		gstages, err := s.groupedStages()
		if err != nil {
			return "", err
		}
		stages = append(stages, gstages...)
	} else {
		pstages, err := s.simpleStages()
		if err != nil {
			return "", err
		}
		stages = append(stages, pstages...)
	}

	return strings.Join(stages, " | "), nil
}

func (s *selectStmt) hasAggregate() bool {
	for _, it := range s.items {
		if !it.star && containsAgg(it.expr) {
			return true
		}
	}
	if s.having != nil && containsAgg(s.having) {
		return true
	}
	return false
}

// onToKeys turns an ON condition (a chain of equalities) into klog join keys.
func onToKeys(n sx) (string, error) {
	var keys []string
	var walk func(sx) error
	walk = func(e sx) error {
		b, ok := e.(sBin)
		if !ok {
			return fmt.Errorf("JOIN ON supports only '=' equalities joined by AND")
		}
		switch b.op {
		case "AND":
			if err := walk(b.l); err != nil {
				return err
			}
			return walk(b.r)
		case "=":
			l := stripQual(b.l)
			r := stripQual(b.r)
			if l == "" || r == "" {
				return fmt.Errorf("JOIN ON must compare columns")
			}
			if l == r {
				keys = append(keys, l)
			} else {
				keys = append(keys, l+" == "+r)
			}
			return nil
		}
		return fmt.Errorf("JOIN ON supports only '=' equalities joined by AND")
	}
	if err := walk(n); err != nil {
		return "", err
	}
	return strings.Join(keys, ", "), nil
}

func stripQual(n sx) string {
	id, ok := n.(sIdent)
	if !ok {
		return ""
	}
	if i := strings.LastIndex(id.name, "."); i >= 0 {
		return id.name[i+1:]
	}
	return id.name
}

func (s *selectStmt) simpleStages() ([]string, error) {
	var stages []string
	// ORDER BY (before project so it can use original columns / aliases)
	aliasExpr := map[string]sx{}
	for _, it := range s.items {
		if it.alias != "" {
			aliasExpr[it.alias] = it.expr
		}
	}
	if len(s.orderBy) > 0 {
		var keys []string
		for _, oi := range s.orderBy {
			e := oi.expr
			if oi.pos > 0 {
				if oi.pos > len(s.items) {
					return nil, fmt.Errorf("ORDER BY position %d out of range", oi.pos)
				}
				e = s.items[oi.pos-1].expr
			} else if id, ok := oi.expr.(sIdent); ok {
				if ae, ok := aliasExpr[id.name]; ok {
					e = ae
				}
			}
			k := e.kql()
			if oi.desc {
				k += " desc"
			} else {
				k += " asc"
			}
			keys = append(keys, k)
		}
		stages = append(stages, "sort by "+strings.Join(keys, ", "))
	}
	// projection
	starOnly := len(s.items) == 1 && s.items[0].star
	var outNames []string
	if !starOnly {
		var decls []string
		for _, it := range s.items {
			if it.star {
				return nil, fmt.Errorf("SELECT * cannot be combined with other columns")
			}
			name := it.alias
			if name != "" {
				decls = append(decls, name+"="+it.expr.kql())
			} else {
				decls = append(decls, it.expr.kql())
				if id, ok := it.expr.(sIdent); ok {
					name = id.name
				} else {
					name = it.expr.kql()
				}
			}
			outNames = append(outNames, name)
		}
		stages = append(stages, "project "+strings.Join(decls, ", "))
	}
	if s.distinct {
		if starOnly {
			stages = append(stages, "distinct *")
		} else {
			stages = append(stages, "distinct "+strings.Join(outNames, ", "))
		}
	}
	if s.hasLimit {
		stages = append(stages, fmt.Sprintf("take %d", s.limit))
	}
	return stages, nil
}

func (s *selectStmt) groupedStages() ([]string, error) {
	var stages []string

	// group keys with names (alias from a matching select item if present)
	keyName := map[string]string{} // key kql -> output name
	var keyDecls []string
	for _, gb := range s.groupBy {
		kexpr := gb.kql()
		name := deriveKeyName(gb)
		for _, it := range s.items {
			if !it.star && it.alias != "" && !containsAgg(it.expr) && it.expr.kql() == kexpr {
				name = it.alias
			}
		}
		keyName[kexpr] = name
		if id, ok := gb.(sIdent); ok && id.name == name {
			keyDecls = append(keyDecls, name)
		} else {
			keyDecls = append(keyDecls, name+"="+kexpr)
		}
	}

	// aggregates: from SELECT, then any extra in HAVING/ORDER
	aggName := map[string]string{} // sig -> name
	used := map[string]bool{}
	var aggDecls []string
	addAgg := func(ar aggRef, preferred string) string {
		if n, ok := aggName[ar.sig]; ok {
			return n
		}
		n := preferred
		if n == "" {
			n = uniqueName(aggAutoName(ar.kqlS), used)
		} else {
			n = uniqueName(n, used)
		}
		aggName[ar.sig] = n
		aggDecls = append(aggDecls, n+"="+ar.kqlS)
		return n
	}

	var selNames []string
	var extendDecls []string
	for _, it := range s.items {
		if it.star {
			return nil, fmt.Errorf("SELECT * is not allowed with GROUP BY or aggregates")
		}
		if ar, ok := aggOf(it.expr); ok {
			selNames = append(selNames, addAgg(ar, it.alias))
			continue
		}
		if containsAgg(it.expr) {
			// scalar expression wrapping aggregate(s): compute aggregates, then
			// derive the value with a post-summarize extend.
			harvestAggs(it.expr, addAgg)
			name := it.alias
			if name == "" {
				name = uniqueName(sqlSanitize(it.expr.kql()), used)
			} else {
				used[name] = true
			}
			extendDecls = append(extendDecls, name+"="+mapAggs(it.expr, aggName).kql())
			selNames = append(selNames, name)
			continue
		}
		if len(s.groupBy) == 0 {
			return nil, fmt.Errorf("column %q must be aggregated or appear in GROUP BY", it.expr.kql())
		}
		if nm, ok := keyName[it.expr.kql()]; ok {
			selNames = append(selNames, nm)
			continue
		}
		// scalar expression over group key columns (e.g. UPPER(service))
		name := it.alias
		if name == "" {
			name = uniqueName(sqlSanitize(it.expr.kql()), used)
		} else {
			used[name] = true
		}
		extendDecls = append(extendDecls, name+"="+it.expr.kql())
		selNames = append(selNames, name)
	}

	// harvest aggregates used only in HAVING/ORDER
	for _, oi := range s.orderBy {
		if oi.expr != nil {
			harvestAggs(oi.expr, addAgg)
		}
	}
	if s.having != nil {
		harvestAggs(s.having, addAgg)
	}

	if len(aggDecls) == 0 {
		return nil, fmt.Errorf("a grouped query needs at least one aggregate")
	}
	summ := "summarize " + strings.Join(aggDecls, ", ")
	if len(keyDecls) > 0 {
		summ += " by " + strings.Join(keyDecls, ", ")
	}
	stages = append(stages, summ)

	if s.having != nil {
		stages = append(stages, "where "+mapAggs(s.having, aggName).kql())
	}

	if len(extendDecls) > 0 {
		stages = append(stages, "extend "+strings.Join(extendDecls, ", "))
	}

	if len(s.orderBy) > 0 {
		var keys []string
		for _, oi := range s.orderBy {
			var k string
			if oi.pos > 0 {
				if oi.pos > len(selNames) {
					return nil, fmt.Errorf("ORDER BY position %d out of range", oi.pos)
				}
				k = selNames[oi.pos-1]
			} else if id, ok := oi.expr.(sIdent); ok && !isKnownColumn(id.name, keyName, aggName) {
				k = id.name // an alias
			} else {
				k = mapAggs(oi.expr, aggName).kql()
			}
			if oi.desc {
				k += " desc"
			} else {
				k += " asc"
			}
			keys = append(keys, k)
		}
		stages = append(stages, "sort by "+strings.Join(keys, ", "))
	}

	stages = append(stages, "project "+strings.Join(selNames, ", "))
	if s.hasLimit {
		stages = append(stages, fmt.Sprintf("take %d", s.limit))
	}
	return stages, nil
}

func isKnownColumn(name string, keyName, aggName map[string]string) bool {
	for _, v := range keyName {
		if v == name {
			return true
		}
	}
	for _, v := range aggName {
		if v == name {
			return true
		}
	}
	return false
}

// harvestAggs finds aggregate calls in an expression and registers them.
func harvestAggs(n sx, add func(aggRef, string) string) {
	if ar, ok := aggOf(n); ok {
		add(ar, "")
		return
	}
	switch t := n.(type) {
	case sBin:
		harvestAggs(t.l, add)
		harvestAggs(t.r, add)
	case sUnary:
		harvestAggs(t.x, add)
	case sBetween:
		harvestAggs(t.x, add)
		harvestAggs(t.lo, add)
		harvestAggs(t.hi, add)
	case sIn:
		harvestAggs(t.x, add)
		for _, a := range t.list {
			harvestAggs(a, add)
		}
	case sIsNull:
		harvestAggs(t.x, add)
	case sLike:
		harvestAggs(t.x, add)
	case sCast:
		harvestAggs(t.x, add)
	case sCall:
		for _, a := range t.args {
			harvestAggs(a, add)
		}
	}
}

// mapAggs returns a copy of the expression with aggregate calls replaced by
// references to their summarized column names.
func mapAggs(n sx, aggName map[string]string) sx {
	if ar, ok := aggOf(n); ok {
		if name, ok := aggName[ar.sig]; ok {
			return sIdent{name}
		}
	}
	switch t := n.(type) {
	case sBin:
		return sBin{t.op, mapAggs(t.l, aggName), mapAggs(t.r, aggName)}
	case sUnary:
		return sUnary{t.op, mapAggs(t.x, aggName)}
	case sBetween:
		return sBetween{mapAggs(t.x, aggName), mapAggs(t.lo, aggName), mapAggs(t.hi, aggName), t.neg}
	case sIn:
		nl := make([]sx, len(t.list))
		for i, a := range t.list {
			nl[i] = mapAggs(a, aggName)
		}
		return sIn{mapAggs(t.x, aggName), nl, t.neg}
	case sIsNull:
		return sIsNull{mapAggs(t.x, aggName), t.neg}
	case sLike:
		return sLike{mapAggs(t.x, aggName), t.pat, t.neg}
	case sCast:
		return sCast{mapAggs(t.x, aggName), t.typ}
	case sCall:
		nargs := make([]sx, len(t.args))
		for i, a := range t.args {
			nargs[i] = mapAggs(a, aggName)
		}
		return sCall{name: t.name, args: nargs, distinct: t.distinct}
	}
	return n
}

func deriveKeyName(n sx) string {
	if id, ok := n.(sIdent); ok {
		if i := strings.LastIndex(id.name, "."); i >= 0 {
			return id.name[i+1:]
		}
		return id.name
	}
	return sqlSanitize(n.kql())
}

func aggAutoName(kqlS string) string {
	open := strings.IndexByte(kqlS, '(')
	if open < 0 {
		return sqlSanitize(kqlS)
	}
	fn := kqlS[:open]
	arg := strings.TrimSuffix(kqlS[open+1:], ")")
	if arg == "" {
		return fn
	}
	return fn + "_" + sqlSanitize(arg)
}

func uniqueName(base string, used map[string]bool) string {
	if base == "" {
		base = "col"
	}
	name := base
	for i := 1; used[name]; i++ {
		name = fmt.Sprintf("%s_%d", base, i)
	}
	used[name] = true
	return name
}

func sqlSanitize(s string) string {
	var b strings.Builder
	prevU := false
	for _, c := range s {
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
			prevU = false
		} else if !prevU {
			b.WriteByte('_')
			prevU = true
		}
	}
	return strings.Trim(b.String(), "_")
}
