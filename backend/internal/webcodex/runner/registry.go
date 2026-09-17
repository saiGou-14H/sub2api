// SPDX-License-Identifier: Apache-2.0
// Adapted from WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9,
// crates/webcodex-runner-registry/src/{runners,polling,registry,requests}.rs.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

// Registry owns in-memory polling leases and synchronous request waiters. It does
// not execute commands or persist results. Callers own durable request identities.
type Registry struct {
	mu           sync.Mutex
	options      Options
	nodes        map[string]*node
	pending      map[string]*request
	jobs         map[string]*jobRecord
	requestToJob map[string]string
	closed       bool
}

// Options bounds the registry's retained nodes and requests. The caller supplies
// operational limits; retired instance count follows the original protocol implementation.
type Options struct {
	MaxRunners          int
	MaxPendingPerRunner int
	OnlineWindow        time.Duration
	// MaxJobsPerRunner bounds active and retained terminal Jobs together; zero disables Job dispatch.
	MaxJobsPerRunner int
	// JobRecoveryGrace is explicit: zero refuses reconciliation-capable registrations.
	JobRecoveryGrace time.Duration
}

const retiredInstanceLimit = 16

var (
	ErrClosed   = errors.New("Runner registry closed")
	ErrStale    = errors.New("Runner instance is stale or replaced")
	ErrUnknown  = errors.New("unknown or expired Runner request")
	ErrCapacity = errors.New("Runner registry capacity reached")
)

type node struct {
	registration   protocol.RunnerRegisterRequest
	access         Access
	registeredAt   int64
	connectedAt    int64
	lastSeen       time.Time
	disconnectedAt *int64
	retired        []string
	queue          []*request
}

type request struct {
	id         string
	clientID   string
	instanceID string
	kind       string
	jobID      string
	wire       []byte
	dispatched bool
	settled    bool
	outcome    Outcome
	done       chan struct{}
}

// Outcome preserves whether delivery happened when completion is uncertain.
// Err with Dispatched=true never proves that a command failed to start.
type Outcome struct {
	Result     *protocol.RunnerResultPayload
	Dispatched bool
	Err        error
}

// Pending owns one synchronous waiter. Wait must be called with a bounded context
// or Cancel must release it. Cancellation cannot terminate a remote process.
type Pending struct {
	registry *Registry
	request  *request
}

// NewRegistry validates limits without starting timers or goroutines.
func NewRegistry(options Options) (*Registry, error) {
	if options.MaxRunners <= 0 || options.MaxPendingPerRunner <= 0 || options.OnlineWindow <= 0 || options.MaxJobsPerRunner < 0 || options.JobRecoveryGrace < 0 {
		return nil, fmt.Errorf("Runner registry limits must be positive")
	}
	return &Registry{options: options, nodes: make(map[string]*node), pending: make(map[string]*request), jobs: make(map[string]*jobRecord), requestToJob: make(map[string]string)}, nil
}

// Register enforces transport identity, generation-2 capabilities and ownership
// before replacing a lease. Same-instance reconnect preserves pending requests;
// a replacement fails them with their original dispatch evidence.
func (r *Registry) Register(principal Principal, input protocol.RunnerRegisterRequest) (protocol.RunnerView, error) {
	var empty protocol.RunnerView
	access, err := principal.authorize(input.ClientID, ScopeRegister)
	if err != nil {
		return empty, err
	}
	owner, err := principal.owner(input.Owner)
	if err != nil {
		return empty, err
	}
	wire, err := json.Marshal(input)
	if err != nil {
		return empty, err
	}
	body, err := protocol.ReadRegisterRequest(wire)
	if err != nil {
		return empty, err
	}
	body.Owner = owner
	if err = protocol.ValidateRegistration(body); err != nil {
		return empty, err
	}
	if body.HostContext != nil {
		normalized, err := body.HostContext.Normalized()
		if err != nil {
			return empty, err
		}
		body.HostContext = &normalized
	}
	if err = protocol.ValidateGenerationCapabilities(body.Capabilities); err != nil {
		return empty, err
	}
	// These optional inventories need their own reconciliation before admission.
	if body.Capabilities.CodingAgentRuns || body.Capabilities.NativeToolPlugins || present(body.CodingAgentInventory) || present(body.CodingAgentProviders) || present(body.Policy) {
		return empty, fmt.Errorf("%w: Runner provider inventory or policy registration", protocol.ErrUnsupported)
	}
	inventory, err := r.registrationInventory(body)
	if err != nil {
		return empty, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return empty, ErrClosed
	}
	prior := r.nodes[body.ClientID]
	if prior != nil && (!permitted(access, prior) || prior.access.GroupKind != access.GroupKind || prior.access.GroupID != access.GroupID) {
		return empty, ErrForbidden
	}
	if prior != nil && prior.registration.AgentInstanceID == body.AgentInstanceID && prior.registration.Capabilities.JobStateReconciliation && !body.Capabilities.JobStateReconciliation {
		return empty, fmt.Errorf("same Runner instance cannot downgrade job_state_reconciliation")
	}
	if prior == nil && len(r.nodes) >= r.options.MaxRunners {
		return empty, ErrCapacity
	}
	if access.GroupKind == "shared_key" && (prior == nil || prior.access.GroupKind != access.GroupKind || prior.access.GroupID != access.GroupID) {
		groupCount, total := 0, 0
		for _, registered := range r.nodes {
			if registered.access.GroupKind == "shared_key" {
				total++
				if registered.access.GroupID == access.GroupID {
					groupCount++
				}
			}
		}
		if groupCount >= 16 || total >= 1024 {
			return empty, ErrCapacity
		}
	}
	if inventory != nil {
		if err := r.preflightInventoryLocked(access, body, *inventory); err != nil {
			return empty, err
		}
	}
	now := time.Now()
	current := &node{registration: body, access: access, registeredAt: now.Unix(), connectedAt: now.Unix(), lastSeen: now}
	if prior != nil {
		for _, id := range prior.retired {
			if id == body.AgentInstanceID {
				return empty, ErrStale
			}
		}
		current.retired = append([]string(nil), prior.retired...)
		if prior.registration.AgentInstanceID == body.AgentInstanceID {
			current.registeredAt = prior.registeredAt
			current.queue = prior.queue
		} else {
			current.retired = append(current.retired, prior.registration.AgentInstanceID)
			if len(current.retired) > retiredInstanceLimit {
				current.retired = current.retired[len(current.retired)-retiredInstanceLimit:]
			}
		}
	}
	view, err := r.viewLocked(current)
	if err != nil {
		return empty, err
	}
	if prior != nil && prior.registration.AgentInstanceID != body.AgentInstanceID {
		r.loseClientJobsLocked(body.ClientID, "runner_instance_replaced", "runner instance was replaced", now)
		r.failClientLocked(body.ClientID, ErrStale)
		view.PendingRequests = 0
	}
	// The same mutex serializes registration preflight, commit, ACK and updates.
	// Inventory is a transient input; the Job records are the sole state owner.
	current.registration.JobInventory = nil
	r.nodes[body.ClientID] = current
	if inventory != nil {
		r.reconcileInventoryLocked(body, *inventory, now)
	}
	view.PendingRequests = uint64(len(current.queue))
	return view, nil
}

func (r *Registry) viewLocked(n *node) (protocol.RunnerView, error) {
	body := n.registration
	pending := uint64(len(n.queue))
	connected := n.disconnectedAt == nil && time.Since(n.lastSeen) <= r.options.OnlineWindow
	status := "stale"
	if connected {
		status = "online"
	}
	view := protocol.RunnerView{ClientID: body.ClientID, AgentInstanceID: body.AgentInstanceID, DisplayName: body.DisplayName, Owner: body.Owner, Hostname: body.Hostname, Status: status, HostContext: body.HostContext, Connected: connected, LastSeen: n.lastSeen.Unix(), Capabilities: body.Capabilities, PendingRequests: pending, Projects: []protocol.RunnerProjectSummary{}, AgentProtocolGeneration: body.AgentProtocolGeneration, Transport: "polling", RegisteredAt: n.registeredAt, ConnectedAt: n.connectedAt, DisconnectedAt: n.disconnectedAt, ProcessStartedAt: body.ProcessStartedAt, Build: body.Build, JobConcurrencyLimit: body.JobConcurrencyLimit}
	// DTO copies prevent a caller mutating the registry's authenticated owner.
	return copyJSON(view)
}

// View returns an isolated snapshot for an authorized caller.
func (r *Registry) View(access Access, clientID string) (protocol.RunnerView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.nodes[clientID]
	if n == nil || !visible(access, n) {
		return protocol.RunnerView{}, ErrForbidden
	}
	return r.viewLocked(n)
}

// Enqueue admits one supported synchronous request for the currently registered
// process. There is no public HTTP dispatch route; host callers must establish
// project/scope authorization and supply a unique durable request ID first.
func (r *Registry) Enqueue(access Access, input protocol.RunnerRequest) (*Pending, error) {
	if err := validateID(input.RequestID, 80, true); err != nil {
		return nil, err
	}
	if err := validateID(input.ClientID, 80, true); err != nil {
		return nil, err
	}
	wire, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	body, err := protocol.ReadRequest(wire)
	if err != nil {
		return nil, err
	}
	if _, err = body.DecodeInvocation(); err != nil {
		return nil, err
	}
	if body.JobID != nil || present(body.JobContext) {
		return nil, fmt.Errorf("%w: Job dispatch", protocol.ErrUnsupported)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, ErrClosed
	}
	n := r.nodes[body.ClientID]
	if n == nil || !permitted(access, n) {
		return nil, ErrForbidden
	}
	if n.disconnectedAt != nil || time.Since(n.lastSeen) > r.options.OnlineWindow {
		return nil, fmt.Errorf("Runner is offline")
	}
	if !supportsRequest(n.registration.Capabilities, body) {
		return nil, fmt.Errorf("Runner capability not advertised")
	}
	if _, ok := r.pending[body.RequestID]; ok || r.requestToJob[body.RequestID] != "" {
		return nil, fmt.Errorf("request_id is already pending")
	}
	count := 0
	for _, p := range r.pending {
		if p.clientID == body.ClientID {
			count++
		}
	}
	if count >= r.options.MaxPendingPerRunner {
		return nil, ErrCapacity
	}
	p := &request{id: body.RequestID, clientID: body.ClientID, instanceID: n.registration.AgentInstanceID, kind: body.Kind, wire: wire, done: make(chan struct{})}
	r.pending[p.id] = p
	n.queue = append(n.queue, p)
	return &Pending{registry: r, request: p}, nil
}

// Poll delivers each queued request once. A failed HTTP response never puts a
// possibly delivered request back into the queue.
func (r *Registry) Poll(principal Principal, body protocol.RunnerPollPayload) (*protocol.RunnerRequest, error) {
	access, err := principal.authorize(body.ClientID, ScopePoll)
	if err != nil {
		return nil, err
	}
	if present(body.ToolProviders) || present(body.ProjectInventoryPage) {
		return nil, fmt.Errorf("%w: polling inventory", protocol.ErrUnsupported)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n, err := r.activeLocked(access, body.ClientID, body.AgentInstanceID)
	if err != nil {
		return nil, err
	}
	n.lastSeen = time.Now()
	n.disconnectedAt = nil
	for len(n.queue) > 0 {
		p := n.queue[0]
		n.queue = n.queue[1:]
		if p.settled {
			continue
		}
		if p.instanceID != body.AgentInstanceID {
			r.settleLocked(p, Outcome{Err: ErrStale})
			continue
		}
		request, err := protocol.ReadRequest(p.wire)
		if err != nil {
			return nil, err
		}
		if !supportsRequest(n.registration.Capabilities, request) {
			if p.jobID != "" {
				if j := r.jobs[p.jobID]; j != nil {
					r.loseJobLocked(j, "runner_capability_withdrawn", "Runner capability withdrawn", time.Now())
				}
			}
			r.settleLocked(p, Outcome{Err: fmt.Errorf("Runner capability withdrawn")})
			continue
		}
		p.dispatched = true
		if p.jobID != "" {
			if p.kind != "stop_job" {
				if job := r.jobs[p.jobID]; job != nil {
					job.dispatched = true
					if jobLifecycle(job) == protocol.JobQueued {
						job.info.Status = protocol.JobRunnerQueued.AsWire()
						job.changed()
					}
				}
			}
			// Ownership now lives on the Job and requestToJob, with no waiter.
			r.settleLocked(p, Outcome{})
		}
		return &request, nil
	}
	return nil, nil
}

// Complete consumes an exact dispatched request for the active instance. A
// successful return means an in-memory waiter accepted it, never a durable ACK.
func (r *Registry) Complete(principal Principal, input protocol.RunnerResultPayload) error {
	access, err := principal.authorize(input.ClientID, ScopeResult)
	if err != nil {
		return err
	}
	body, err := copyJSON(input)
	if err != nil {
		return err
	}
	if present(body.MCPGateway) || present(body.PluginGateway) || present(body.CodingAgent) {
		return fmt.Errorf("%w: gateway result", protocol.ErrUnsupported)
	}
	if err = validateID(body.RequestID, 80, true); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n, err := r.activeLocked(access, body.ClientID, body.AgentInstanceID)
	if err != nil {
		return err
	}
	if id := r.requestToJob[body.RequestID]; id != "" {
		if j := r.jobs[id]; j != nil && (j.info.ClientID != body.ClientID || j.instanceID != body.AgentInstanceID) {
			return ErrForbidden
		}
		return fmt.Errorf("%w: Job results require job_update", protocol.ErrUnsupported)
	}
	p := r.pending[body.RequestID]
	if p == nil {
		return ErrUnknown
	}
	if p.clientID != body.ClientID || p.instanceID != body.AgentInstanceID {
		return ErrForbidden
	}
	if !p.dispatched {
		return fmt.Errorf("Runner request has not been dispatched")
	}
	if p.jobID != "" {
		return fmt.Errorf("%w: Job results require job_update", protocol.ErrUnsupported)
	}
	if strings.HasPrefix(p.kind, "file_") && body.CommandExecutionState != nil {
		return fmt.Errorf("command_execution_state is only valid for command requests")
	}
	body.Stdout = truncateOutput(body.Stdout)
	body.Stderr = truncateOutput(body.Stderr)
	n.lastSeen = time.Now()
	r.settleLocked(p, Outcome{Result: &body})
	return nil
}

// Offline is an instance-scoped, idempotent lifecycle notice. A delayed notice
// from a replaced process cannot disconnect the new process.
func (r *Registry) Offline(principal Principal, body protocol.RunnerOfflineRequest) error {
	access, err := principal.authorize(body.ClientID, ScopeRegister)
	if err != nil {
		return err
	}
	if err = validateID(body.ClientID, 80, true); err != nil {
		return err
	}
	if err = validateID(body.AgentInstanceID, 128, false); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	n := r.nodes[body.ClientID]
	if n == nil || !permitted(access, n) {
		return ErrForbidden
	}
	if n.registration.AgentInstanceID != body.AgentInstanceID {
		return nil
	}
	now := time.Now().Unix()
	n.disconnectedAt = &now
	n.lastSeen = time.Now().Add(-r.options.OnlineWindow - time.Second)
	for _, j := range r.jobs {
		if j.info.ClientID != body.ClientID {
			continue
		}
		if n.registration.Capabilities.JobStateReconciliation && j.dispatched {
			r.beginRecoveryLocked(j, time.Now(), "runner_transport_disconnected")
		} else if n.registration.Capabilities.JobStateReconciliation && !j.dispatched {
			r.loseJobLocked(j, "runner_request_not_dispatched", "Runner transport disconnected before queued request was dispatched", time.Now())
		} else {
			r.loseJobLocked(j, "runner_disconnected_without_reconciliation", "Runner disconnected", time.Now())
		}
	}
	r.failClientLocked(body.ClientID, fmt.Errorf("Runner disconnected"))
	n.queue = nil
	return nil
}

func (r *Registry) activeLocked(access Access, clientID, instanceID string) (*node, error) {
	if r.closed {
		return nil, ErrClosed
	}
	if err := validateID(clientID, 80, true); err != nil {
		return nil, err
	}
	if err := validateID(instanceID, 128, false); err != nil {
		return nil, err
	}
	n := r.nodes[clientID]
	if n == nil || !permitted(access, n) {
		return nil, ErrForbidden
	}
	if n.registration.AgentInstanceID != instanceID {
		return nil, ErrStale
	}
	return n, nil
}

func (r *Registry) settleLocked(p *request, outcome Outcome) {
	if p.settled {
		return
	}
	outcome.Dispatched = p.dispatched
	p.outcome = outcome
	p.settled = true
	if r.pending[p.id] == p {
		delete(r.pending, p.id)
	}
	// Drop cancelled queue entries immediately to bound retention even without polls.
	if n := r.nodes[p.clientID]; n != nil {
		for i, item := range n.queue {
			if item == p {
				n.queue = append(n.queue[:i], n.queue[i+1:]...)
				break
			}
		}
	}
	p.wire = nil
	if p.done != nil {
		close(p.done)
	}
}

func (r *Registry) failClientLocked(clientID string, err error) {
	for _, p := range r.pending {
		if p.clientID == clientID {
			r.settleLocked(p, Outcome{Err: err})
		}
	}
}

// Wait returns the one final outcome. Context cancellation releases the waiter
// while retaining the observed dispatch flag; it never retries the request.
func (p *Pending) Wait(ctx context.Context) Outcome {
	select {
	case <-p.request.done:
	case <-ctx.Done():
		p.Cancel(ctx.Err())
	}
	p.registry.mu.Lock()
	defer p.registry.mu.Unlock()
	result := p.request.outcome
	if result.Result != nil {
		value, err := copyJSON(*result.Result)
		if err != nil {
			return Outcome{Dispatched: result.Dispatched, Err: err}
		}
		result.Result = &value
	}
	return result
}

// Cancel settles an outstanding waiter and leaves a completed outcome unchanged.
func (p *Pending) Cancel(reason error) {
	if reason == nil {
		reason = context.Canceled
	}
	p.registry.mu.Lock()
	defer p.registry.mu.Unlock()
	p.registry.settleLocked(p.request, Outcome{Err: reason})
}

// Close settles all waiters and cancels owned recovery deadlines. It owns no
// remote processes or sockets.
func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	for _, p := range r.pending {
		r.settleLocked(p, Outcome{Err: ErrClosed})
	}
	for _, j := range r.jobs {
		j.stopRecoveryTimer()
	}
	r.nodes = make(map[string]*node)
	r.jobs = make(map[string]*jobRecord)
	r.requestToJob = make(map[string]string)
}

// supportsRequest combines the host mutation matrix with the original generic
// selector scan (requests.rs:328-357,391-415,503-557). Callers hold r.mu through
// admission/dispatch. Semantic payload validation remains Runner-owned.
func supportsRequest(c protocol.RunnerCapabilities, request protocol.RunnerRequest) bool {
	if !supports(c, request.Kind) {
		return false
	}
	if request.Kind != "file_apply_text_edits" || request.Content == nil {
		return true
	}
	if !selectorJSONEligible(*request.Content) {
		return true
	}
	// RawMessage keeps integer lexemes lossless and map decoding selects the
	// last duplicate member, as serde_json::Value does in the source scanner.
	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(*request.Content), &payload) != nil {
		return true
	}
	var changes []json.RawMessage
	if json.Unmarshal(payload["changes"], &changes) != nil {
		return true
	}
	requiresOccurrence, requiresLineScope := false, false
	for _, change := range changes {
		var fields map[string]json.RawMessage
		if json.Unmarshal(change, &fields) != nil {
			continue
		}
		var edits []json.RawMessage
		if json.Unmarshal(fields["edits"], &edits) != nil {
			continue
		}
		for _, edit := range edits {
			var selectors map[string]json.RawMessage
			if json.Unmarshal(edit, &selectors) != nil {
				continue
			}
			requiresOccurrence = requiresOccurrence || present(selectors["occurrence"])
			requiresLineScope = requiresLineScope || present(selectors["line_scope"])
		}
	}
	// Generic ingress does not call the dedicated occurrence-only entry point.
	return !requiresLineScope || (c.ApplyTextEditLineScope && (!requiresOccurrence || c.ApplyTextEditOccurrence))
}

// selectorJSONEligible checks the whole Value before inspecting selectors.
// serde_json 1.0.150 rejects lone surrogates, non-finite numbers and a 128th
// nested container, even in unknown or subsequently overwritten members.
// This private Unicode check mirrors protocol.validateJSONUnicode; exporting a
// protocol API just for this registry scan would expand the wire codec surface.
func selectorJSONEligible(content string) bool {
	if !utf8.ValidString(content) {
		return false
	}
	quoted := false
	for i := 0; i < len(content); i++ {
		if content[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || content[i] != '\\' {
			continue
		}
		i++
		if i >= len(content) {
			return false
		}
		if content[i] != 'u' {
			continue
		}
		if i+4 >= len(content) {
			return false
		}
		n, err := strconv.ParseUint(content[i+1:i+5], 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n < 0xd800 || n > 0xdbff {
			continue
		}
		if i+6 >= len(content) || content[i+1] != '\\' || content[i+2] != 'u' {
			return false
		}
		low, err := strconv.ParseUint(content[i+3:i+7], 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	// Token's default number conversion rejects overflow to infinity. Values
	// here are discarded: the separate RawMessage scan and wire retain the
	// original numeric lexemes (including exact u64/i64 and wider finite values).
	decoder := json.NewDecoder(strings.NewReader(content))
	depth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{', '[':
				depth++
				if depth >= 128 {
					return false
				}
			case '}', ']':
				depth--
			}
		}
	}
}

func supports(c protocol.RunnerCapabilities, kind string) bool {
	switch kind {
	case "start_process_job":
		return (c.AsyncJobs || c.AsyncShellJobs) && c.StructuredExecutionJobs && c.StructuredProcessArgv
	case "stop_job":
		return c.AsyncJobs || c.AsyncShellJobs
	case "run_shell":
		return c.Shell
	case "run_process":
		return c.StructuredProcessArgv
	case "run_script":
		return c.StructuredScriptPayload
	case "file_read", "file_list", "file_skill_read_file", "file_skill_list_packages", "file_project_overview":
		return c.FileRead
	case "file_write", "file_write_project_file", "file_apply_text_edits":
		// Host mutation-matrix adaptation; the frozen generic enqueue has no
		// explicit FileWrite gate (requests.rs:436-455).
		return c.FileWrite
	case "file_apply_patch":
		// The host matrix adopts the dedicated entry's three-bit gate
		// (requests.rs:583-605); the source generic entry has no patch gate.
		return c.ApplyPatch && c.ApplyPatchMatchMetadata && c.ApplyPatchMatchingMode
	case "file_delete_project_files":
		return c.StructuredFileDelete
	default:
		return false
	}
}

func present(raw json.RawMessage) bool {
	return len(raw) != 0 && strings.TrimSpace(string(raw)) != "null"
}

func validateID(value string, limit int, dot bool) error {
	if len(value) == 0 || len(value) > limit {
		return fmt.Errorf("Runner identity length is invalid")
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || dot && r == '.') {
			return fmt.Errorf("Runner identity contains invalid characters")
		}
	}
	return nil
}

// The original per-stream limit retains the UTF-8 tail plus its truncation
// notice. The complete HTTP body has a separate configured ingress bound.
const maxOutputBytes = 256 * 1024

func truncateOutput(value *string) *string {
	if value == nil || len(*value) <= maxOutputBytes {
		return value
	}
	start := len(*value) - maxOutputBytes
	for start < len(*value) && !utf8.RuneStart((*value)[start]) {
		start++
	}
	result := fmt.Sprintf("[output truncated to last %d bytes]\n%s", maxOutputBytes, (*value)[start:])
	return &result
}

func copyJSON[T any](input T) (T, error) {
	var result T
	bytes, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(bytes, &result)
	return result, err
}
