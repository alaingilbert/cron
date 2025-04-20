package httpManagement

import (
	"bytes"
	"embed"
	"github.com/alaingilbert/cron"
	"github.com/alaingilbert/cron/internal/utils"
	"html/template"
	"net/http"
	"slices"
	"time"
)

//go:embed templates/* css/*
var fs embed.FS

func GetMux(c *cron.Cron) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", indexHandler(c))
	mux.HandleFunc("POST /{$}", indexHandler(c))
	mux.HandleFunc("GET /entries/{entryID}/{$}", entryHandler(c))
	mux.HandleFunc("POST /entries/{entryID}/{$}", entryHandler(c))
	mux.HandleFunc("GET /entries/{entryID}/runs/{runID}/{$}", runHandler(c))
	mux.HandleFunc("POST /entries/{entryID}/runs/{runID}/{$}", runHandler(c))
	return mux
}

func getCss() string {
	style, _ := fs.ReadFile("css/style.css")
	return string(style)
}

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

func redirectTo(w http.ResponseWriter, l string) {
	w.Header().Set("Location", l)
	w.WriteHeader(http.StatusSeeOther)
}

func render(code int, name string, data any, w http.ResponseWriter) {
	tmplHtml, _ := fs.ReadFile(name)
	tmpl, _ := template.New("").Funcs(funcsMap).Parse(string(tmplHtml))
	var b bytes.Buffer
	_ = tmpl.Execute(&b, data)
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(code)
	_, _ = w.Write(b.Bytes())
}

type indexData struct {
	Css     template.CSS
	Menu    template.HTML
	JobRuns []cron.JobRun
	Entries []cron.Entry
}

type entryData struct {
	Css              template.CSS
	Menu             template.HTML
	Entry            cron.Entry
	JobRuns          []cron.JobRun
	CompletedJobRuns []cron.JobRun
}

type runData struct {
	Css    template.CSS
	Menu   template.HTML
	JobRun cron.JobRun
	Entry  cron.Entry
}

func indexHandler(c *cron.Cron) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
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
			redirectTo(w, "/")
			return
		}
		jobRuns := c.RunningJobs()
		entries := c.Entries()
		data := indexData{
			JobRuns: jobRuns,
			Entries: entries,
			Css:     template.CSS(getCss()),
			Menu:    template.HTML(getMenu(c)),
		}
		render(http.StatusOK, "templates/index.gohtml", data, w)
	}
}

func entryHandler(c *cron.Cron) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entryID := cron.EntryID(r.PathValue("entryID"))
		entry, err := c.Entry(entryID)
		if err != nil {
			redirectTo(w, "/")
			return
		}
		if r.Method == http.MethodPost {
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
			redirectTo(w, "/entries/"+string(entryID))
			return
		}

		jobRuns, _ := c.RunningJobsFor(entryID)
		completedJobRuns, _ := c.CompletedJobRunsFor(entryID)
		slices.Reverse(completedJobRuns)
		data := entryData{
			Entry:            entry,
			JobRuns:          jobRuns,
			CompletedJobRuns: completedJobRuns,
			Css:              template.CSS(getCss()),
			Menu:             template.HTML(getMenu(c)),
		}
		render(http.StatusOK, "templates/entry.gohtml", data, w)
	}
}

func runHandler(c *cron.Cron) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entryID := cron.EntryID(r.PathValue("entryID"))
		runID := cron.RunID(r.PathValue("runID"))
		entry, entryErr := c.Entry(entryID)
		jobRun, runErr := c.GetJobRun(entryID, runID)
		if entryErr != nil || runErr != nil {
			redirectTo(w, "/")
			return
		}
		if r.Method == http.MethodPost {
			formName := r.PostFormValue("formName")
			if formName == "cancelRun" {
				_ = c.CancelJobRun(entry.ID, jobRun.RunID)
				redirectTo(w, "/entries/"+string(entryID))
				return
			}
			redirectTo(w, "/entries/"+string(entryID))
			return
		}
		data := runData{
			JobRun: jobRun,
			Entry:  entry,
			Css:    template.CSS(getCss()),
			Menu:   template.HTML(getMenu(c)),
		}
		render(http.StatusOK, "templates/run.gohtml", data, w)
	}
}
