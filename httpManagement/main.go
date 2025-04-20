package httpManagement

import (
	"bytes"
	"github.com/alaingilbert/cron"
	"github.com/alaingilbert/cron/internal/utils"
	"html/template"
	"net/http"
	"slices"
	"time"
)

func GetMux(c *cron.Cron) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", getIndexHandler(c))
	mux.HandleFunc("POST /{$}", postIndexHandler(c))
	mux.HandleFunc("GET /entries/{entryID}/{$}", getEntryHandler(c))
	mux.HandleFunc("POST /entries/{entryID}/{$}", postEntryHandler(c))
	mux.HandleFunc("GET /entries/{entryID}/runs/{runID}/{$}", getRunHandler(c))
	mux.HandleFunc("POST /entries/{entryID}/runs/{runID}/{$}", postRunHandler(c))
	return mux
}

var css = `
<style>
* {
	-webkit-box-sizing: border-box;
	box-sizing: border-box;
}

body {
	color-scheme: light dark;
	font-family: -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Helvetica Neue",Arial,sans-serif,"Apple Color Emoji","Segoe UI Emoji","Segoe UI Symbol";
	line-height: 1.75em;
	font-size: 16px;
	background-color: #222;
	color: #aaa;
}

.simple-container {
	max-width: 675px;
	margin: 0 auto;
	padding-top: 70px;
	padding-bottom: 20px;
}

.simple-print {
	fill: white;
	stroke: white;
}
.simple-print svg {
	height: 100%;
}

.simple-close {
	color: white;
	border-color: white;
}

.simple-ext-info {
	border-top: 1px solid #aaa;
}

p {
	font-size: 16px;
}

h1 {
	font-size: 30px;
	line-height: 34px;
}

h2 {
	font-size: 20px;
	line-height: 25px;
}

h3 {
	font-size: 16px;
	line-height: 27px;
	padding-top: 15px;
	padding-bottom: 15px;
	border-bottom: 1px solid #D8D8D8;
	border-top: 1px solid #D8D8D8;
}

hr {
	height: 1px;
	background-color: #d8d8d8;
	border: none;
	width: 100%;
	margin: 0px;
}

a[href] {
	color: #1e8ad6;
}

a[href]:hover {
	color: #3ba0e6;
}

img {
	max-width: 100%;
}

li {
	line-height: 1.5em;
}

aside,
[class *= "sidebar"],
[id *= "sidebar"] {
	max-width: 90%;
	margin: 0 auto;
	border: 1px solid lightgrey;
	padding: 5px 15px;
}

@media (min-width: 1921px) {
	body {
		font-size: 18px;
	}
}
	form {
		margin: 0;
	}
	table td {
		padding: 0 5px;
	}
	.monospace { font-family: monospace; }
	a {
		color: #eee;
	}
	.d-inline-block { display: inline-block; }
	.success { color: #0f0; }
	.danger { color: #f00; }
	.mb-1 { margin-bottom: 10px; }
	small { color: #666; }
	.text-center { text-align: center; }
</style>
`

func getMenu(c *cron.Cron) string {
	out := `
<a href="/">home</a>
<hr />
Current time: ` + time.Now().Format(time.DateTime) + `<br />
Last cleanup: 
`
	if c.GetCleanupTS().IsZero() {
		out += `-`
	} else {
		out += c.GetCleanupTS().Format(time.DateTime) + ` <small>(` + utils.ShortDur(c.GetCleanupTS()) + `)</small>`
	}
	out += `
<hr />`
	return out
}

var funcsMap = template.FuncMap{
	"FmtDate":  func(t time.Time) string { return t.Format(time.DateTime) },
	"ShortDur": func(t time.Time) string { return utils.ShortDur(t) },
}

func getIndexHandler(c *cron.Cron) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var b bytes.Buffer
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		jobRuns := c.RunningJobs()
		entries := c.Entries()

		tmpl, _ := template.New("").Funcs(funcsMap).Parse(`
<!doctype html>
<html>
<head>
	` + css + `
</head>
<body>
` + getMenu(c) + `
Running jobs ({{ len .JobRuns }})<br />
<table>
	<thead>
		<th>Entry ID</th>
		<th>Run ID</th>
		<th>Label</th>
		<th>Spec</th>
		<th>Started at</th>
		<th>Actions</th>
	</thead>
	<tbody>
		{{ range .JobRuns }}
			<tr>
				<td><span class="monospace"><a href="/entries/{{ .Entry.ID }}">{{ .Entry.ID }}</a></span></td>
				<td><span class="monospace"><a href="/entries/{{ .Entry.ID }}/runs/{{ .RunID }}">{{ .RunID }}</a></span></td>
				<td>{{ .Entry.Label }}</td>
				<td>{{ if .Entry.Spec }}{{ .Entry.Spec }}{{ else }}-{{ end }}</td>
				<td>{{ .StartedAt | FmtDate }} <small>({{ .StartedAt | ShortDur }})</small></td>
				<td>
					<form method="POST" class="d-inline-block">
						<input type="hidden" name="formName" value="cancelRun" />
						<input type="hidden" name="entryID" value="{{ .Entry.ID }}" />
						<input type="hidden" name="runID" value="{{ .RunID }}" />
						<input type="submit" value="Cancel" />
					</form>
				</td>
			</tr>
		{{ else }}
			<tr><td colspan="6"><em>No running jobs</em></td></tr>
		{{ end }}
	</tbody>
</table>


<hr />

Entries ({{ len .Entries }})<br />

<table>
	<thead>
		<tr>
			<th>Entry ID</th>
			<th>Label</th>
			<th>Spec</th>
			<th>Prev</th>
			<th>Next</th>
			<th>Active</th>
			<th>Actions</th>
		</tr>
	</thead>
	<tbody>
		{{ range .Entries }}
			<tr>
				<td><span class="monospace"><a href="/entries/{{ .ID }}">{{ .ID }}</a></span></td>
				<td>{{ if .Label }}{{ .Label }}{{ else }}-{{ end }}</td>
				<td>{{ if .Spec }}{{ .Spec }}{{ else }}-{{ end }}</td>
				<td>{{ .Prev | FmtDate }}</td>
				<td>{{ .Next | FmtDate }}{{ if not .Next.IsZero }} <small>({{ .Next | ShortDur }})</small>{{ end }}</td>
				<td class="text-center">
					{{ if .Active }}
						<span class="success">T</span>
					{{ else }}
						<span class="danger">F</span>
					{{ end }}
				</td>
				<td>
					<form method="POST" class="d-inline-block">
						<input type="hidden" name="formName" value="runNow" />
						<input type="hidden" name="entryID" value="{{ .ID }}" />
						<input type="submit" value="Run now"{{ if not .Active }} disabled{{ end }} />
					</form>
					{{ if .Active }}
						<form method="POST" class="d-inline-block">
							<input type="hidden" name="formName" value="disableEntry" />
							<input type="hidden" name="entryID" value="{{ .ID }}" />
							<input type="submit" value="Disable" />
						</form>
					{{ else }}
						<form method="POST" class="d-inline-block">
							<input type="hidden" name="formName" value="enableEntry" />
							<input type="hidden" name="entryID" value="{{ .ID }}" />
							<input type="submit" value="Enable" />
						</form>
					{{ end }}
					<form method="POST" class="d-inline-block">
						<input type="hidden" name="formName" value="removeEntry" />
						<input type="hidden" name="entryID" value="{{ .ID }}" />
						<input type="submit" value="Remove" />
					</form>
				</td>
			</tr>
		{{ else }}
			<tr><td colspan="7"><em>No entries</em></td></tr>
		{{ end }}
	</tbody>
</table>
</body>
</html>
`)
		_ = tmpl.Execute(&b, map[string]any{
			"JobRuns": jobRuns,
			"Entries": entries,
		})
		_, _ = w.Write(b.Bytes())
	}
}

func postIndexHandler(c *cron.Cron) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		formName := r.PostFormValue("formName")
		if formName == "cancelRun" {
			entryID := cron.EntryID(r.PostFormValue("entryID"))
			runID := cron.RunID(r.PostFormValue("runID"))
			_ = c.CancelJobRun(entryID, runID)
		} else if formName == "removeEntry" {
			entryID := cron.EntryID(r.PostFormValue("entryID"))
			c.Remove(entryID)
		} else if formName == "enableEntry" {
			entryID := cron.EntryID(r.PostFormValue("entryID"))
			c.Enable(entryID)
		} else if formName == "disableEntry" {
			entryID := cron.EntryID(r.PostFormValue("entryID"))
			c.Disable(entryID)
		} else if formName == "runNow" {
			entryID := cron.EntryID(r.PostFormValue("entryID"))
			_ = c.RunNow(entryID)
		}
		w.Header().Set("Location", "/")
		w.WriteHeader(http.StatusSeeOther)
	}
}

func getEntryHandler(c *cron.Cron) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entryID := cron.EntryID(r.PathValue("entryID"))
		entry, err := c.Entry(entryID)
		if err != nil {
			w.Header().Set("Location", "/")
			w.WriteHeader(http.StatusSeeOther)
			return
		}
		var b bytes.Buffer
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		jobRuns, _ := c.RunningJobsFor(entryID)
		completedJobRuns, _ := c.CompletedJobRunsFor(entryID)
		slices.Reverse(completedJobRuns)
		tmpl, _ := template.New("").Funcs(funcsMap).Parse(`
<!doctype html>
<html>
<head>
	` + css + `
</head>
<body>
` + getMenu(c) + `
<table class="mb-1">
	<tr><td>Entry ID:</td><td><span class="monospace">{{ .Entry.ID }}</span></td></tr>
	<tr><td>Label:</td><td>{{ .Entry.Label }}</td></tr>
	<tr><td>Spec:</td><td>{{ if .Entry.Spec }}{{ .Entry.Spec }}{{ else }}-{{ end }}</td></tr>
	<tr><td>Active:</td><td>{{ if .Entry.Active }}<span class="success">T</span>{{ else }}<span class="danger">F</span>{{ end }}</td></tr>
	<tr><td>Prev:</td><td>{{ .Entry.Prev | FmtDate }}{{ if not .Entry.Prev.IsZero }} <small>({{ .Entry.Prev | ShortDur }})</small>{{ end }}</td></tr>
	<tr><td>Next:</td><td>{{ .Entry.Next | FmtDate }}{{ if not .Entry.Next.IsZero }} <small>({{ .Entry.Next | ShortDur }})</small>{{ end }}</td></tr>
</table>
<hr />
<div class="mb-1">
{{ if .Entry.Active }}
		<form method="POST" class="d-inline-block">
			<input type="hidden" name="formName" value="disableEntry" />
			<input type="submit" value="Disable" />
		</form>
{{ else }}
		<form method="POST" class="d-inline-block">
			<input type="hidden" name="formName" value="enableEntry" />
			<input type="submit" value="Enable" />
		</form>
{{ end }}
</div>

<div class="mb-1">
	<label for="label">Job label:</label>
	<form method="POST">
		<input type="hidden" name="formName" value="updateLabel" />
		<input type="text" name="label" value="{{ .Entry.Label }}" placeholder="Label" />
		<input type="submit" value="Update label" id="label" />
	</form>
</div>

<div class="mb-1">
	<label for="spec">Job spec:</label>
	<form method="POST">
		<input type="hidden" name="formName" value="updateSpec" />
		<input type="text" name="spec" value="{{ .Entry.Spec }}" placeholder="Spec" />
		<input type="submit" value="Update spec" id="spec" />
	</form>
</div>
<hr />
Running jobs ({{ len .JobRuns }})<br />
<table class="mb-1">
	<thead>
		<th>Run ID</th>
		<th>Label</th>
		<th>Started at</th>
		<th>Actions</th>
	</thead>
	<tbody>
		{{ range .JobRuns }}
			<tr>
				<td><span class="monospace"><a href="/entries/{{ .Entry.ID }}/runs/{{ .RunID }}">{{ .RunID }}</a></span></td>
				<td>{{ if .Entry.Label }}{{ .Entry.Label }}{{ else }}-{{ end }}</td>
				<td>
					<span class="monospace">{{ .StartedAt | FmtDate }}</span>
					<small>({{ .StartedAt | ShortDur }})</small>
				</td>
				<td>
					<form method="POST" class="d-inline-block">
						<input type="hidden" name="formName" value="cancelRun" />
						<input type="hidden" name="runID" value="{{ .RunID }}" />
						<input type="submit" value="Cancel" />
					</form>
				</td>
			</tr>
		{{ else }}
			<tr><td colspan="4"><em>No running jobs</em></td></tr>
		{{ end }}
	</tbody>
</table>

Completed jobs ({{ len .CompletedJobRuns }})<br />
<table class="mb-1">
	<thead>
		<th>Run ID</th>
		<th>Label</th>
		<th>Started at</th>
		<th>Completed at</th>
		<th>Error</th>
		<th>Panic</th>
	</thead>
	<tbody>
		{{ range .CompletedJobRuns }}
			<tr>
				<td><span class="monospace"><a href="/entries/{{ .Entry.ID }}/runs/{{ .RunID }}">{{ .RunID }}</a></span></td>
				<td>{{ if .Entry.Label }}{{ .Entry.Label }}{{ else }}-{{ end }}</td>
				<td><span class="monospace">{{ .StartedAt | FmtDate }}</span> <small>({{ .StartedAt | ShortDur }})</small></td>
				<td><span class="monospace">{{ .CompletedAt | FmtDate }}</span> <small>({{ .CompletedAt | ShortDur }})</small></td>
				<td>
					{{ if .Error }}
						<span class="danger" title="{{ .Error.Error }}">Error</span>
					{{ else }}
						<span>-</span>
					{{ end }}
				</td>
				<td>
					{{ if .Panic }}
						<span class="danger">Panic</span>
					{{ else }}
						<span>-</span>
					{{ end }}
				</td>
			</tr>
		{{ else }}
			<tr><td colspan="6"><em>No completed jobs</em></td></tr>
		{{ end }}
	</tbody>
</table>

</body>
</html>
`)
		_ = tmpl.Execute(&b, map[string]any{
			"Entry":            entry,
			"JobRuns":          jobRuns,
			"CompletedJobRuns": completedJobRuns,
		})
		_, _ = w.Write(b.Bytes())
	}
}

func postEntryHandler(c *cron.Cron) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entryID := cron.EntryID(r.PathValue("entryID"))
		_, err := c.Entry(entryID)
		if err != nil {
			w.Header().Set("Location", "/")
			w.WriteHeader(http.StatusSeeOther)
			return
		}
		formName := r.PostFormValue("formName")
		if formName == "updateLabel" {
			label := r.PostFormValue("label")
			c.UpdateLabel(entryID, label)
		} else if formName == "updateSpec" {
			spec := r.PostFormValue("spec")
			_ = c.UpdateScheduleWithSpec(entryID, spec)
		} else if formName == "enableEntry" {
			c.Enable(entryID)
		} else if formName == "disableEntry" {
			c.Disable(entryID)
		} else if formName == "cancelRun" {
			runID := cron.RunID(r.PostFormValue("runID"))
			_ = c.CancelJobRun(entryID, runID)
		}
		w.Header().Set("Location", "/entries/"+string(entryID))
		w.WriteHeader(http.StatusSeeOther)
	}
}

func getRunHandler(c *cron.Cron) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entryID := cron.EntryID(r.PathValue("entryID"))
		runID := cron.RunID(r.PathValue("runID"))
		entry, err := c.Entry(entryID)
		if err != nil {
			w.Header().Set("Location", "/")
			w.WriteHeader(http.StatusSeeOther)
			return
		}
		jobRun, err := c.GetJobRun(entryID, runID)
		if err != nil {
			w.Header().Set("Location", "/")
			w.WriteHeader(http.StatusSeeOther)
			return
		}
		var b bytes.Buffer
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		tmpl, _ := template.New("").Funcs(funcsMap).Parse(`
<!doctype html>
<html>
<head>
	` + css + `
</head>
<body>
` + getMenu(c) + `
<table class="mb-1">
	<tr><td>Run ID:</td><td><span class="monospace">{{ .JobRun.RunID }}</span></td></tr>
	<tr><td>Entry ID:</td><td><span class="monospace"><a href="/entries/{{ .Entry.ID }}">{{ .Entry.ID }}</a></span></td></tr>
	<tr><td>Label:</td><td>{{ if .Entry.ID }}{{ .Entry.Label }}{{ else }}-{{ end }}</td></tr>
	<tr><td>Spec:</td><td>{{ if .Entry.Spec }}{{ .Entry.Spec }}{{ else }}-{{ end }}</td></tr>
</table>
<div class="mb-1">
	<form method="POST">
		<input type="hidden" name="formName" value="cancelRun" />
		<input type="hidden" name="entryID" value="{{ .JobRun.Entry.ID }}" />
		<input type="hidden" name="runID" value="{{ .JobRun.RunID }}" />
		<input type="submit" value="Cancel"{{ if .JobRun.CompletedAt }} disabled{{ end }} />
	</form>
</div>
<hr />
Events ({{ len .JobRun.Events }})<br />
<table>
	<thead>
		<tr>
			<th>Type</th>
			<th>Created at</th>
		</tr>
	</thead>
	<tbody>
		{{ range .JobRun.Events }}
			<tr>
				<td>{{ .Typ }}</td>
				<td>
					<span class="monospace">{{ .CreatedAt | FmtDate }}</span>
					<small>({{ .CreatedAt | ShortDur }})</small>
				</td>
			</tr>
		{{ else }}
			<tr><td colspan="2"><em>No events</em></td></tr>
		{{ end }}
	</tbody>
</table>
</body>
</html>
`)
		_ = tmpl.Execute(&b, map[string]any{
			"JobRun": jobRun,
			"Entry":  entry,
		})
		_, _ = w.Write(b.Bytes())
	}
}

func postRunHandler(c *cron.Cron) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entryID := cron.EntryID(r.PathValue("entryID"))
		runID := cron.RunID(r.PathValue("runID"))
		entry, err := c.Entry(entryID)
		if err != nil {
			w.Header().Set("Location", "/")
			w.WriteHeader(http.StatusSeeOther)
			return
		}
		jobRun, err := c.GetJobRun(entryID, runID)
		if err != nil {
			w.Header().Set("Location", "/")
			w.WriteHeader(http.StatusSeeOther)
			return
		}
		formName := r.PostFormValue("formName")
		if formName == "cancelRun" {
			_ = c.CancelJobRun(entry.ID, jobRun.RunID)
			w.Header().Set("Location", "/entries/"+string(entryID))
			w.WriteHeader(http.StatusSeeOther)
			return
		}
		w.Header().Set("Location", "/entries/"+string(entryID))
		w.WriteHeader(http.StatusSeeOther)
	}
}
