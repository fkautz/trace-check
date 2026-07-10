package tracecheck

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const problemReportSchemaVersion = 1

// ProblemReport is the deterministic machine-readable diagnostic report
// emitted by Scope.ProblemsJSON, including on a failed check.
type ProblemReport struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Complete      bool                   `json:"complete"`
	GeneratedBy   string                 `json:"generatedBy"`
	Profile       string                 `json:"profile,omitempty"`
	ToolVersion   string                 `json:"toolVersion,omitempty"`
	ConfigDigest  string                 `json:"configDigest"`
	Scope         ProblemReportScope     `json:"scope"`
	Artifacts     ProblemReportArtifacts `json:"artifacts"`
	Evidence      ProblemReportEvidence  `json:"evidence"`
	Summary       ProblemReportSummary   `json:"summary"`
	Problems      []ProblemJSON          `json:"problems"`
}

// ProblemReportArtifacts identifies the named control artifacts reconciled by
// the check. Coverage source files are represented by their discovered tags,
// not by an aggregate source-tree digest.
type ProblemReportArtifacts struct {
	Config         *ProblemReportArtifact `json:"config,omitempty"`
	Catalog        *ProblemReportArtifact `json:"catalog,omitempty"`
	Classification *ProblemReportArtifact `json:"classification,omitempty"`
	Waivers        *ProblemReportArtifact `json:"waivers,omitempty"`
	Architecture   *ProblemReportArtifact `json:"architecture,omitempty"`
}

// ProblemReportArtifact records a normalized path and the content digest of
// the exact bytes supplied to the parser. Present is false when an optional or
// fatally missing input cannot be read; an incomplete report then prevents
// consumers from trusting it.
type ProblemReportArtifact struct {
	Path    string `json:"path"`
	Present bool   `json:"present"`
	SHA256  string `json:"sha256,omitempty"`
}

// ProblemReportScope records the effective gate, including CLI/profile
// overrides, so a baseline cannot be reused under a weaker scope.
type ProblemReportScope struct {
	Strict         bool     `json:"strict"`
	Phases         []string `json:"phases"`
	KeywordClasses []string `json:"keywordClasses"`
	CheckOutput    bool     `json:"checkOutput"`
	MatrixMarkdown string   `json:"matrixMarkdown,omitempty"`
	MatrixJSON     string   `json:"matrixJSON,omitempty"`
}

// ProblemReportEvidence records the successful coverage evidence consumed by
// the check. The sorted records and their digest make tag deletion, movement,
// test renaming, and coverage-class changes visible to ratchet consumers.
type ProblemReportEvidence struct {
	TagsSHA256 string             `json:"tagsSha256"`
	Tags       []ProblemReportTag `json:"tags"`
}

type ProblemReportTag struct {
	Requirement string `json:"requirement"`
	File        string `json:"file"`
	Test        string `json:"test"`
	Class       string `json:"class"`
}

// ProblemReportSummary splits release-backlog findings from integrity errors.
type ProblemReportSummary struct {
	Total       int `json:"total"`
	Baselinable int `json:"baselinable"`
	Integrity   int `json:"integrity"`
}

// ProblemJSON is one stable diagnostic. Baselinable is true only for an
// expected strict-coverage backlog item; integrity/configuration drift must
// never be accepted into a development baseline.
type ProblemJSON struct {
	Key         string `json:"key"`
	Code        string `json:"code"`
	Requirement string `json:"requirement,omitempty"`
	Message     string `json:"message"`
	Baselinable bool   `json:"baselinable"`
}

type problemClassifier struct {
	fullID                  *regexp.Regexp
	coverageRequired        *regexp.Regexp
	policyClassRequired     *regexp.Regexp
	classifiedClassRequired *regexp.Regexp
	waiverReasonRejected    *regexp.Regexp
	coveredTargetMissing    *regexp.Regexp
}

func newProblemClassifier(c *Config) problemClassifier {
	id := `(?:` + c.IDGrammar.Pattern + `)`
	return problemClassifier{
		fullID:                  c.compiled.fullID,
		coverageRequired:        regexp.MustCompile(`^` + id + ` \(.*\): no tagged test or waiver$`),
		policyClassRequired:     regexp.MustCompile(`^` + id + ` \(.*\): policy requires .+ coverage$`),
		classifiedClassRequired: regexp.MustCompile(`^` + id + ` \(.*\): .* but has no .+ coverage$`),
		waiverReasonRejected:    regexp.MustCompile(`^` + id + ` \(.*\): waiver reason "[^"]+" does not satisfy strict coverage$`),
		coveredTargetMissing:    regexp.MustCompile(`^` + id + ` \(.*\): covered-by target ` + id + ` has no tagged test for strict coverage$`),
	}
}

func (c *Config) problemReport(scope Scope, messages []string, complete bool) (ProblemReport, error) {
	scope = normalizeScopePaths(scope)
	return c.problemReportWithArtifacts(scope, messages, complete, c.problemArtifacts(scope), nil)
}

func (c *Config) problemReportWithArtifacts(scope Scope, messages []string, complete bool, artifacts ProblemReportArtifacts, tags map[string][]TagRef) (ProblemReport, error) {
	scope = normalizeScopePaths(scope)
	phases := append([]string(nil), c.effectiveStrictPhases(scope)...)
	classes := append([]string(nil), c.effectiveStrictKeywordClasses(scope)...)
	sort.Strings(phases)
	sort.Strings(classes)
	configDigest, err := c.configDigest()
	if err != nil {
		return ProblemReport{}, err
	}
	evidence, err := problemEvidence(tags)
	if err != nil {
		return ProblemReport{}, err
	}
	report := ProblemReport{
		SchemaVersion: problemReportSchemaVersion,
		Complete:      complete,
		GeneratedBy:   plainGeneratedBy(c.Matrix.GeneratedBy),
		Profile:       scope.Profile,
		ToolVersion:   scope.ToolVersion,
		ConfigDigest:  configDigest,
		Scope: ProblemReportScope{
			Strict:         scope.Strict,
			Phases:         phases,
			KeywordClasses: classes,
			CheckOutput:    scope.CheckOutput,
			MatrixMarkdown: problemOutputPath(scope.Root, scope.Out),
			MatrixJSON:     problemOutputPath(scope.Root, scope.OutJSON),
		},
		Artifacts: artifacts,
		Evidence:  evidence,
		Problems:  make([]ProblemJSON, 0, len(messages)),
	}
	classifier := newProblemClassifier(c)
	byKey := map[string]ProblemJSON{}
	for _, message := range messages {
		problem := classifier.classify(message)
		if existing, ok := byKey[problem.Key]; !ok || problem.Message < existing.Message {
			byKey[problem.Key] = problem
		}
	}
	for _, problem := range byKey {
		report.Problems = append(report.Problems, problem)
	}
	report.Summary.Total = len(report.Problems)
	sort.Slice(report.Problems, func(i, j int) bool {
		if report.Problems[i].Key != report.Problems[j].Key {
			return report.Problems[i].Key < report.Problems[j].Key
		}
		return report.Problems[i].Message < report.Problems[j].Message
	})
	for _, problem := range report.Problems {
		if problem.Baselinable {
			report.Summary.Baselinable++
		} else {
			report.Summary.Integrity++
		}
	}
	return report, nil
}

func problemOutputPath(root, path string) string {
	if path == "" {
		return ""
	}
	return reportPath(root, resolveScopePath(root, path))
}

func problemEvidence(tags map[string][]TagRef) (ProblemReportEvidence, error) {
	records := make([]ProblemReportTag, 0)
	for requirement, refs := range tags {
		for _, ref := range refs {
			records = append(records, ProblemReportTag{
				Requirement: requirement,
				File:        ref.File,
				Test:        ref.Func,
				Class:       ref.Class,
			})
		}
	}
	sort.Slice(records, func(i, j int) bool {
		left, right := records[i], records[j]
		if left.Requirement != right.Requirement {
			return left.Requirement < right.Requirement
		}
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Test != right.Test {
			return left.Test < right.Test
		}
		return left.Class < right.Class
	})
	data, err := json.Marshal(records)
	if err != nil {
		return ProblemReportEvidence{}, fmt.Errorf("marshal coverage evidence: %w", err)
	}
	sum := sha256.Sum256(data)
	return ProblemReportEvidence{
		TagsSHA256: "sha256:" + hex.EncodeToString(sum[:]),
		Tags:       records,
	}, nil
}

type artifactRead struct {
	data []byte
	err  error
}

// artifactReader is one immutable view of the named control artifacts used by
// a Check. Parsers and provenance share this cache, so a complete report hashes
// the exact bytes reconciled even if a file changes later in the run.
type artifactReader struct {
	root  string
	reads map[string]artifactRead
}

func newArtifactReader(root string) *artifactReader {
	return &artifactReader{root: root, reads: map[string]artifactRead{}}
}

func (r *artifactReader) readFile(path string) ([]byte, error) {
	key := artifactPathKey(path)
	if read, ok := r.reads[key]; ok {
		return read.data, read.err
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is operator-supplied
	r.reads[key] = artifactRead{data: data, err: err}
	return data, err
}

func artifactPathKey(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(absolute)
}

func (r *artifactReader) artifact(path string) *ProblemReportArtifact {
	if path == "" {
		return nil
	}
	entry := &ProblemReportArtifact{Path: reportPath(r.root, path)}
	read, ok := r.reads[artifactPathKey(path)]
	if !ok || read.err != nil {
		return entry
	}
	sum := sha256.Sum256(read.data)
	entry.Present = true
	entry.SHA256 = "sha256:" + hex.EncodeToString(sum[:])
	return entry
}

func (r *artifactReader) artifacts(c *Config, scope Scope) ProblemReportArtifacts {
	configPath, _ := c.effectiveConfigPath(scope)
	config := r.artifact(configPath)
	if c.sourcePath != "" && c.sourceSHA256 != "" {
		config = &ProblemReportArtifact{
			Path:    reportPath(scope.Root, c.sourcePath),
			Present: true,
			SHA256:  c.sourceSHA256,
		}
	}
	return ProblemReportArtifacts{
		Config:         config,
		Catalog:        r.artifact(scope.Catalog),
		Classification: r.artifact(scope.Classification),
		Waivers:        r.artifact(scope.Waivers),
		Architecture:   r.artifact(c.effectiveArchitecturePath(scope)),
	}
}

func (c *Config) problemArtifacts(scope Scope) ProblemReportArtifacts {
	scope = normalizeScopePaths(scope)
	return newArtifactReader(scope.Root).artifacts(c, scope)
}

func reportPath(root, path string) string {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	absoluteRoot, err := filepath.Abs(root)
	if err == nil {
		if relative, relErr := filepath.Rel(absoluteRoot, absolutePath); relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(relative)
		}
	}
	return filepath.ToSlash(absolutePath)
}

func (c *Config) classifyProblem(message string) ProblemJSON {
	return newProblemClassifier(c).classify(message)
}

func (c problemClassifier) classify(message string) ProblemJSON {
	problem := ProblemJSON{Code: "integrity", Message: message}
	id := c.requirement(message)

	switch {
	case id != "" && c.coverageRequired.MatchString(message):
		problem.Code = "coverage-required"
		problem.Key = problem.Code + ":" + id
		problem.Requirement = id
		problem.Baselinable = true
	case id != "" && c.policyClassRequired.MatchString(message):
		class := textBetweenLast(message, ": policy requires ", " coverage")
		problem.Code = "coverage-class-required"
		problem.Key = problem.Code + ":" + id + ":" + class
		problem.Requirement = id
		problem.Baselinable = true
	case id != "" && c.classifiedClassRequired.MatchString(message):
		class := textBetweenLast(message, " but has no ", " coverage")
		problem.Code = "coverage-class-required"
		problem.Key = problem.Code + ":" + id + ":" + class
		problem.Requirement = id
		problem.Baselinable = true
	case id != "" && c.waiverReasonRejected.MatchString(message):
		reason := textBetweenLast(message, `: waiver reason "`, `" does not satisfy strict coverage`)
		problem.Code = "waiver-reason-not-accepted"
		problem.Key = problem.Code + ":" + id + ":" + reason
		problem.Requirement = id
		problem.Baselinable = true
	case id != "" && c.coveredTargetMissing.MatchString(message):
		target := textBetweenLast(message, ": covered-by target ", " has no tagged test for strict coverage")
		problem.Code = "waiver-proxy-not-covered"
		problem.Key = problem.Code + ":" + id + ":" + target
		problem.Requirement = id
		problem.Baselinable = true
	default:
		sum := sha256.Sum256([]byte(message))
		problem.Key = "integrity:" + hex.EncodeToString(sum[:])
	}
	return problem
}

func (c problemClassifier) requirement(message string) string {
	fields := strings.Fields(message)
	if len(fields) == 0 {
		return ""
	}
	candidate := strings.TrimSuffix(fields[0], ":")
	if c.fullID.MatchString(candidate) {
		return candidate
	}
	return ""
}

func textBetweenLast(s, prefix, suffix string) string {
	start := strings.LastIndex(s, prefix)
	if start < 0 || !strings.HasSuffix(s, suffix) {
		return ""
	}
	start += len(prefix)
	return strings.TrimSuffix(s[start:], suffix)
}

func (c *Config) configDigest() (string, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("marshal config for diagnostic provenance: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (c *Config) writeProblemsJSONTo(output *atomicOutput, scope Scope, messages []string, complete bool, artifacts ProblemReportArtifacts, tags map[string][]TagRef) error {
	report, err := c.problemReportWithArtifacts(scope, messages, complete, artifacts, tags)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := output.WriteFile(data, 0o600); err != nil {
		return fmt.Errorf("write problems JSON %s: %w", output.path, err)
	}
	return nil
}
