package server

/* buildJobFromForm assembles a storage.Job from the Phase
 * 17.1 form view's per-field inputs. The form is one input
 * per top-level schema item; nested lists with deep shape
 * (parameters / steps / axis / triggers_downstream) come in
 * as YAML-fragment textareas so the V1 form does not have
 * to manage repeating UI rows for everything. Triggers are
 * a 3-row exception with a type dropdown - the canonical
 * "finite set of options" field the operator asked for.
 */

import (
    "fmt"
    "html/template"
    "net/http"
    "strconv"
    "strings"

    "gopkg.in/yaml.v3"

    "github.com/kaleb-himes/wolfCI/internal/storage"
)

/* jobFormFuncs is the template.FuncMap the create/edit page
 * uses. Registered once at server New so every page render
 * sees the same set; per-page Clone+ParseFS inherits them.
 */
func jobFormFuncs() template.FuncMap {
    return template.FuncMap{
        "inc": func(i int) int { return i + 1 },
        "triggerTypeFor": func(j *storage.Job, idx int) string {
            if j == nil || idx >= len(j.Triggers) {
                return ""
            }
            return j.Triggers[idx].Type
        },
        "triggerConfigFor": func(j *storage.Job, idx int) string {
            if j == nil || idx >= len(j.Triggers) || j.Triggers[idx].Config == nil {
                return ""
            }
            t := j.Triggers[idx]
            if v, ok := t.Config["schedule"]; ok {
                return v
            }
            if v, ok := t.Config["config"]; ok {
                return v
            }
            /* Fallback: pick any single value so an
             * operator-typed key still surfaces.
             */
            for _, v := range t.Config {
                return v
            }
            return ""
        },
        "stepsYAML":              func(j *storage.Job) string { return marshalListFragment(jobStepsOrNil(j)) },
        "parametersYAML":         func(j *storage.Job) string { return marshalListFragment(jobParamsOrNil(j)) },
        "axisYAML":               func(j *storage.Job) string { return marshalListFragment(jobAxisOrNil(j)) },
        "triggersDownstreamYAML": func(j *storage.Job) string { return marshalListFragment(jobTDOrNil(j)) },

        /* 18.29 GHPRB section accessors: each returns the
         * value from the job's GitHubPRBTrigger block, or
         * the zero value when no GHPRB is configured. The
         * poll-interval helper substitutes the canonical
         * 300-second default so the rendered input is never
         * blank for a freshly-created job. */
        "ghprbAPICredsFor": func(j *storage.Job) string {
            if j == nil || j.GitHubPRB == nil {
                return ""
            }
            return j.GitHubPRB.APICredentialsID
        },
        "ghprbGHProjectURLFor": func(j *storage.Job) string {
            if j == nil || j.GitHubPRB == nil {
                return ""
            }
            return j.GitHubPRB.GHProjectURL
        },
        "ghprbAdminUsersFor": func(j *storage.Job) string {
            if j == nil || j.GitHubPRB == nil {
                return ""
            }
            return strings.Join(j.GitHubPRB.AdminUsers, "\n")
        },
        "ghprbBranchesFor": func(j *storage.Job) string {
            if j == nil || j.GitHubPRB == nil {
                return ""
            }
            return strings.Join(j.GitHubPRB.BranchesToBuild, "\n")
        },
        "ghprbPollIntervalFor": func(j *storage.Job) int {
            if j == nil || j.GitHubPRB == nil ||
                j.GitHubPRB.PollIntervalSeconds == 0 {
                return 300
            }
            return j.GitHubPRB.PollIntervalSeconds
        },

        /* 18.29 Pipeline-from-SCM panel accessors. Read out
         * of the storage.Pipeline block; nil pipeline (or
         * non-from_scm Definition) means every field is
         * empty so the form renders blank for an operator
         * starting from scratch. */
        "scmRepoURLFor": func(j *storage.Job) string {
            if scm := jobSCMOrNil(j); scm != nil {
                return scm.RepoURL
            }
            return ""
        },
        "scmCredsFor": func(j *storage.Job) string {
            if scm := jobSCMOrNil(j); scm != nil {
                return scm.CredentialsID
            }
            return ""
        },
        "scmBranchFor": func(j *storage.Job) string {
            if scm := jobSCMOrNil(j); scm != nil {
                return scm.BranchSpecifier
            }
            return ""
        },
        "scmScriptPathFor": func(j *storage.Job) string {
            if scm := jobSCMOrNil(j); scm != nil {
                return scm.ScriptPath
            }
            return ""
        },
        "scmLightweightFor": func(j *storage.Job) bool {
            if scm := jobSCMOrNil(j); scm != nil {
                return scm.LightweightCheckout
            }
            return false
        },

        /* 18.28 BuildEnv checkboxes - the form rendered
         * these via 18.27's general-options block, but the
         * UI surface lands here so the section sits next to
         * GHPRB / SCM. Reads the *BuildEnv block; nil yields
         * unchecked for each. */
        "buildEnvPrep": func(j *storage.Job) bool {
            if j == nil || j.BuildEnv == nil {
                return false
            }
            return j.BuildEnv.PrepareEnvForRun
        },
        "buildEnvKeepJEnv": func(j *storage.Job) bool {
            if j == nil || j.BuildEnv == nil {
                return false
            }
            return j.BuildEnv.KeepJenkinsEnvVars
        },
        "buildEnvKeepBuild": func(j *storage.Job) bool {
            if j == nil || j.BuildEnv == nil {
                return false
            }
            return j.BuildEnv.KeepJenkinsBuildVars
        },
    }
}

/* jobSCMOrNil returns the storage.SCMConfig block when the
 * job has a from_scm pipeline definition, or nil otherwise.
 * Keeps the FuncMap helpers above to one liners that only
 * read the populated case. */
func jobSCMOrNil(j *storage.Job) *storage.SCMConfig {
    if j == nil || j.Pipeline == nil {
        return nil
    }
    if j.Pipeline.Definition != "from_scm" {
        return nil
    }
    return j.Pipeline.SCM
}

func jobStepsOrNil(j *storage.Job) interface{} {
    if j == nil || len(j.Steps) == 0 {
        return nil
    }
    return j.Steps
}
func jobParamsOrNil(j *storage.Job) interface{} {
    if j == nil || len(j.Parameters) == 0 {
        return nil
    }
    return j.Parameters
}
func jobAxisOrNil(j *storage.Job) interface{} {
    if j == nil || len(j.Axis) == 0 {
        return nil
    }
    return j.Axis
}
func jobTDOrNil(j *storage.Job) interface{} {
    if j == nil || len(j.TriggersDownstream) == 0 {
        return nil
    }
    return j.TriggersDownstream
}

/* marshalListFragment renders a single list as a YAML
 * fragment for the form's textareas. yaml.Marshal of a slice
 * emits "- item\n- item\n" form which is what an operator
 * expects to see (and what buildJobFromForm reads back).
 * Empty or nil input renders empty so the textarea is blank
 * rather than showing "null".
 */
func marshalListFragment(v interface{}) string {
    if v == nil {
        return ""
    }
    data, err := yaml.Marshal(v)
    if err != nil {
        return ""
    }
    return strings.TrimRight(string(data), "\n")
}

/* knownTriggerTypes is the set of values the trigger.type
 * dropdown offers. Empty selects no trigger in that row.
 * Anything outside this list is refused at parse time so
 * the dropdown stays the canonical entry point.
 */
var knownTriggerTypes = []string{"cron", "webhook", "scm"}

/* formTriggerRows is how many trigger slots the form
 * renders. Operators who need more rows can drop down to
 * the raw YAML view or extend this constant later.
 */
const formTriggerRows = 3

func buildJobFromForm(r *http.Request) (*storage.Job, error) {
    if err := r.ParseForm(); err != nil {
        return nil, fmt.Errorf("parse form: %w", err)
    }
    job := &storage.Job{
        Name:             strings.TrimSpace(r.FormValue("name")),
        Description:      r.FormValue("description"),
        NodeLabel:        strings.TrimSpace(r.FormValue("node_label")),
        Timeout:          strings.TrimSpace(r.FormValue("timeout")),
        GitHubProjectURL: strings.TrimSpace(r.FormValue("github_project_url")),
    }
    if job.Name == "" {
        return nil, fmt.Errorf("name is required")
    }
    if v := strings.TrimSpace(r.FormValue("retries")); v != "" {
        n, err := strconv.Atoi(v)
        if err != nil {
            return nil, fmt.Errorf("retries: %w", err)
        }
        job.Retries = n
    }

    /* Retention is optional; the block is set only when at
     * least one of the sub-fields was filled in. Two form
     * surfaces drive the same storage.Retention block:
     *
     *   - retention_max_builds + retention_max_age: the
     *     pre-18.27 surface, kept verbatim so existing form
     *     state and tests still round-trip.
     *   - discard_old_builds.* : the Jenkins-aligned form
     *     names 18.27 introduced. discard_old_builds.strategy
     *     accepts "log_rotation" as a marker (the only
     *     strategy wolfCI ships - Retention IS log rotation);
     *     max_builds maps to Retention.MaxBuilds verbatim;
     *     days_to_keep is normalised to "<N>d" so it lands
     *     in Retention.MaxAge alongside the existing
     *     duration-string form.
     *
     * If both surfaces are populated the discard_old_builds.*
     * inputs win - the new form has explicit per-field rows
     * and the older inputs are convenience aliases for
     * raw-YAML migrations.
     */
    mb := strings.TrimSpace(r.FormValue("retention_max_builds"))
    ma := strings.TrimSpace(r.FormValue("retention_max_age"))
    dobStrat := strings.TrimSpace(
        r.FormValue("discard_old_builds.strategy"))
    dobMaxBuilds := strings.TrimSpace(
        r.FormValue("discard_old_builds.max_builds"))
    dobDays := strings.TrimSpace(
        r.FormValue("discard_old_builds.days_to_keep"))
    if dobStrat != "" && dobStrat != "log_rotation" {
        return nil, fmt.Errorf(
            "discard_old_builds.strategy %q is not "+
                "supported (only %q)",
            dobStrat, "log_rotation")
    }
    if dobMaxBuilds != "" {
        mb = dobMaxBuilds
    }
    if dobDays != "" {
        ma = dobDays + "d"
    }
    if mb != "" || ma != "" {
        ret := &storage.Retention{MaxAge: ma}
        if mb != "" {
            n, err := strconv.Atoi(mb)
            if err != nil {
                return nil, fmt.Errorf("retention_max_builds: %w", err)
            }
            ret.MaxBuilds = n
        }
        job.Retention = ret
    }

    /* Upstream is one job name per line. Blank lines are
     * dropped so an operator can format the textarea with
     * blank padding without poisoning the list.
     */
    if v := r.FormValue("upstream"); strings.TrimSpace(v) != "" {
        for _, line := range strings.Split(v, "\n") {
            line = strings.TrimSpace(line)
            if line != "" {
                job.Upstream = append(job.Upstream, line)
            }
        }
    }

    /* Trigger rows: empty type drops the row, otherwise
     * the type must be one of the known options.
     */
    for i := 0; i < formTriggerRows; i++ {
        t := strings.TrimSpace(
            r.FormValue(fmt.Sprintf("trigger_%d_type", i)))
        if t == "" {
            continue
        }
        if !contains(knownTriggerTypes, t) {
            return nil, fmt.Errorf("trigger_%d_type %q is "+
                "not one of %v", i, t, knownTriggerTypes)
        }
        cfg := strings.TrimSpace(
            r.FormValue(fmt.Sprintf("trigger_%d_config", i)))
        trig := storage.Trigger{Type: t}
        if cfg != "" {
            /* "cron" carries the schedule under "schedule";
             * other types use a free-form config under
             * "config" so we do not silently lose what the
             * operator typed.
             */
            key := "config"
            if t == "cron" {
                key = "schedule"
            }
            trig.Config = map[string]string{key: cfg}
        }
        job.Triggers = append(job.Triggers, trig)
    }

    /* 18.28 BuildEnv toggles. Each field is opt-in; the
     * block stays nil when none of the three is on so the
     * legacy YAML round-trip is unchanged. Form checkboxes
     * submit "true" / "on" when checked, nothing when not;
     * accept either to be friendly to JS-driven forms that
     * emit "true". */
    prepEnv := isFormChecked(r.FormValue("prepare_environment_for_run"))
    keepEnv := isFormChecked(r.FormValue("keep_jenkins_environment_variables"))
    keepBuild := isFormChecked(r.FormValue("keep_jenkins_build_variables"))
    if prepEnv || keepEnv || keepBuild {
        job.BuildEnv = &storage.BuildEnv{
            PrepareEnvForRun:     prepEnv,
            KeepJenkinsEnvVars:   keepEnv,
            KeepJenkinsBuildVars: keepBuild,
        }
    }

    /* 18.29 GHPRB section. Empty api_credentials_id (the
     * canonical "trigger disabled" marker) leaves
     * job.GitHubPRB nil so a job without GHPRB
     * configuration round-trips clean YAML. */
    if apiCred := strings.TrimSpace(
        r.FormValue("api_credentials_id")); apiCred != "" {
        ghprb := &storage.GitHubPRBTrigger{
            APICredentialsID: apiCred,
            GHProjectURL: strings.TrimSpace(
                r.FormValue("gh_project_url")),
        }
        for _, line := range strings.Split(
            r.FormValue("admin_users"), "\n") {
            line = strings.TrimSpace(line)
            if line != "" {
                ghprb.AdminUsers = append(
                    ghprb.AdminUsers, line)
            }
        }
        for _, line := range strings.Split(
            r.FormValue("branches_to_build"), "\n") {
            line = strings.TrimSpace(line)
            if line != "" {
                ghprb.BranchesToBuild = append(
                    ghprb.BranchesToBuild, line)
            }
        }
        if v := strings.TrimSpace(
            r.FormValue("poll_interval_seconds")); v != "" {
            n, err := strconv.Atoi(v)
            if err != nil {
                return nil, fmt.Errorf(
                    "poll_interval_seconds: %w", err)
            }
            ghprb.PollIntervalSeconds = n
        }
        job.GitHubPRB = ghprb
    }

    /* 18.29 Pipeline-from-SCM panel. Empty repo_url is the
     * canonical "disabled" marker; the block stays nil so
     * non-pipeline jobs round-trip the same YAML they did
     * before 18.29. */
    if repoURL := strings.TrimSpace(
        r.FormValue("repo_url")); repoURL != "" {
        scm := &storage.SCMConfig{
            RepoURL: repoURL,
            CredentialsID: strings.TrimSpace(
                r.FormValue("credentials_id")),
            BranchSpecifier: strings.TrimSpace(
                r.FormValue("branch_specifier")),
            ScriptPath: strings.TrimSpace(
                r.FormValue("script_path")),
            LightweightCheckout: isFormChecked(
                r.FormValue("lightweight_checkout")),
        }
        job.Pipeline = &storage.PipelineBlock{
            Definition: "from_scm",
            SCM:        scm,
        }
    }

    /* Deep-list YAML fragments. Each unmarshal is into the
     * concrete slice type so a syntax error fails the form
     * with the schema-level reason instead of a wrapping
     * "could not parse" string.
     */
    if v := strings.TrimSpace(r.FormValue("steps_yaml")); v != "" {
        var steps []storage.Step
        if err := yaml.Unmarshal([]byte(v), &steps); err != nil {
            return nil, fmt.Errorf("steps_yaml: %w", err)
        }
        job.Steps = steps
    }
    if v := strings.TrimSpace(r.FormValue("parameters_yaml")); v != "" {
        var params []storage.Parameter
        if err := yaml.Unmarshal([]byte(v), &params); err != nil {
            return nil, fmt.Errorf("parameters_yaml: %w", err)
        }
        job.Parameters = params
    }
    if v := strings.TrimSpace(r.FormValue("axis_yaml")); v != "" {
        var axis []storage.AxisDimension
        if err := yaml.Unmarshal([]byte(v), &axis); err != nil {
            return nil, fmt.Errorf("axis_yaml: %w", err)
        }
        job.Axis = axis
    }
    if v := strings.TrimSpace(
        r.FormValue("triggers_downstream_yaml")); v != "" {
        var td []storage.TriggerSpec
        if err := yaml.Unmarshal([]byte(v), &td); err != nil {
            return nil, fmt.Errorf("triggers_downstream_yaml: %w", err)
        }
        job.TriggersDownstream = td
    }
    return job, nil
}

func contains(haystack []string, needle string) bool {
    for _, h := range haystack {
        if h == needle {
            return true
        }
    }
    return false
}

/* isFormChecked maps an HTML checkbox / boolean form value to
 * a Go bool. HTML forms submit nothing when a checkbox is
 * unchecked and "on" when checked; some JS-driven forms set
 * "true" / "1" explicitly. Accept all three "on" / "true" /
 * "1" forms so the builder is friendly to either path. */
func isFormChecked(v string) bool {
    switch strings.ToLower(strings.TrimSpace(v)) {
    case "on", "true", "1", "yes":
        return true
    }
    return false
}
