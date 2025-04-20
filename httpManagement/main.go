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
	mux.HandleFunc("GET /{$}", getIndexHandler(c))
	mux.HandleFunc("POST /{$}", postIndexHandler(c))
	mux.HandleFunc("GET /entries/{entryID}/{$}", getEntryHandler(c))
	mux.HandleFunc("POST /entries/{entryID}/{$}", postEntryHandler(c))
	mux.HandleFunc("GET /entries/{entryID}/runs/{runID}/{$}", getRunHandler(c))
	mux.HandleFunc("POST /entries/{entryID}/runs/{runID}/{$}", postRunHandler(c))
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

func getIndexHandler(c *cron.Cron) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var b bytes.Buffer
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		jobRuns := c.RunningJobs()
		entries := c.Entries()
		tmplHtml, _ := fs.ReadFile("templates/index.gohtml")
		tmpl, _ := template.New("").Funcs(funcsMap).Parse(string(tmplHtml))
		_ = tmpl.Execute(&b, map[string]any{
			"JobRuns": jobRuns,
			"Entries": entries,
			"Css":     template.CSS(getCss()),
			"Menu":    template.HTML(getMenu(c)),
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
		redirectTo(w, "/")
	}
}

func getEntryHandler(c *cron.Cron) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entryID := cron.EntryID(r.PathValue("entryID"))
		entry, err := c.Entry(entryID)
		if err != nil {
			redirectTo(w, "/")
			return
		}
		var b bytes.Buffer
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		jobRuns, _ := c.RunningJobsFor(entryID)
		completedJobRuns, _ := c.CompletedJobRunsFor(entryID)
		slices.Reverse(completedJobRuns)
		tmplHtml, _ := fs.ReadFile("templates/entry.gohtml")
		tmpl, _ := template.New("").Funcs(funcsMap).Parse(string(tmplHtml))
		_ = tmpl.Execute(&b, map[string]any{
			"Entry":            entry,
			"JobRuns":          jobRuns,
			"CompletedJobRuns": completedJobRuns,
			"Css":              template.CSS(getCss()),
			"Menu":             template.HTML(getMenu(c)),
		})
		_, _ = w.Write(b.Bytes())
	}
}

func postEntryHandler(c *cron.Cron) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entryID := cron.EntryID(r.PathValue("entryID"))
		_, err := c.Entry(entryID)
		if err != nil {
			redirectTo(w, "/")
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
		redirectTo(w, "/entries/"+string(entryID))
	}
}

func getRunHandler(c *cron.Cron) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entryID := cron.EntryID(r.PathValue("entryID"))
		runID := cron.RunID(r.PathValue("runID"))
		entry, err := c.Entry(entryID)
		if err != nil {
			redirectTo(w, "/")
			return
		}
		jobRun, err := c.GetJobRun(entryID, runID)
		if err != nil {
			redirectTo(w, "/")
			return
		}
		var b bytes.Buffer
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		tmplHtml, _ := fs.ReadFile("templates/run.gohtml")
		tmpl, _ := template.New("").Funcs(funcsMap).Parse(string(tmplHtml))
		_ = tmpl.Execute(&b, map[string]any{
			"JobRun": jobRun,
			"Entry":  entry,
			"Css":    template.CSS(getCss()),
			"Menu":   template.HTML(getMenu(c)),
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
			redirectTo(w, "/")
			return
		}
		jobRun, err := c.GetJobRun(entryID, runID)
		if err != nil {
			redirectTo(w, "/")
			return
		}
		formName := r.PostFormValue("formName")
		if formName == "cancelRun" {
			_ = c.CancelJobRun(entry.ID, jobRun.RunID)
			redirectTo(w, "/entries/"+string(entryID))
			return
		}
		redirectTo(w, "/entries/"+string(entryID))
	}
}
