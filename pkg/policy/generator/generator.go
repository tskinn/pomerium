// Package generator converts Pomerium Policy Language into Rego.
package generator

import (
	"fmt"
	"sort"

	"github.com/open-policy-agent/opa/ast"

	"github.com/pomerium/pomerium/pkg/policy/parser"
)

// A Generator generates a rego script from a policy.
type Generator struct {
	ids      map[string]int
	criteria map[string]Criterion
}

// An Option configures the Generator.
type Option func(*Generator)

// WithCriterion adds a Criterion to the generator's known criteria.
func WithCriterion(criterionConstructor CriterionConstructor) Option {
	return func(g *Generator) {
		c := criterionConstructor(g)
		for _, name := range c.Names() {
			g.criteria[name] = c
		}
	}
}

// New creates a new Generator.
func New(options ...Option) *Generator {
	g := &Generator{
		ids:      make(map[string]int),
		criteria: make(map[string]Criterion),
	}
	for _, o := range options {
		o(g)
	}
	return g
}

// GetCriterion gets a Criterion for the given name.
func (g *Generator) GetCriterion(name string) (Criterion, bool) {
	c, ok := g.criteria[name]
	return c, ok
}

// Generate generates the rego module from a policy.
func (g *Generator) Generate(policy *parser.Policy) (*ast.Module, error) {
	rules := ast.NewRuleSet()
	rules.Add(ast.MustParseRule(`default allow = false`))
	rules.Add(ast.MustParseRule(`default deny = false`))

	for _, policyRule := range policy.Rules {
		rule := &ast.Rule{
			Head: &ast.Head{
				Name:  ast.Var(policyRule.Action),
				Value: ast.VarTerm("v"),
			},
		}

		fields := []struct {
			criteria  []parser.Criterion
			generator conditionalGenerator
		}{
			{policyRule.And, g.generateAndRule},
			{policyRule.Or, g.generateOrRule},
			{policyRule.Not, g.generateNotRule},
			{policyRule.Nor, g.generateNorRule},
		}
		for _, field := range fields {
			if len(field.criteria) == 0 {
				continue
			}
			subRule, err := field.generator(&rules, field.criteria)
			if err != nil {
				return nil, err
			}
			if len(rule.Body) == 0 {
				rule.Body = append(rule.Body, ast.Assign.Expr(ast.VarTerm("v"), ast.VarTerm(string(subRule.Head.Name))))
			}
			rule.Body = append(rule.Body, ast.NewExpr(ast.VarTerm(string(subRule.Head.Name))))
		}

		rules.Add(rule)
	}

	mod := &ast.Module{
		Package: &ast.Package{
			Path: ast.Ref{
				ast.StringTerm("policy.rego"),
				ast.StringTerm("pomerium"),
				ast.StringTerm("policy"),
			},
		},
		Rules: rules,
	}

	// move functions to the end
	sort.SliceStable(mod.Rules, func(i, j int) bool {
		return len(mod.Rules[i].Head.Args) < len(mod.Rules[j].Head.Args)
	})

	i := 1
	ast.WalkRules(mod, func(r *ast.Rule) bool {
		r.SetLoc(ast.NewLocation([]byte(r.String()), "", i, 1))
		i++
		return false
	})

	return mod, nil
}

// NewRule creates a new rule with a dynamically generated name.
func (g *Generator) NewRule(name string) *ast.Rule {
	id := g.ids[name]
	g.ids[name]++
	return &ast.Rule{
		Head: &ast.Head{
			Name: ast.Var(fmt.Sprintf("%s_%d", name, id)),
		},
	}
}
