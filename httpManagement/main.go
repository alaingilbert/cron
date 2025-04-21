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
	mux.Handle("GET  /{$}", indexHandler(c))
	mux.Handle("POST /{$}", indexHandler(c))
	mux.Handle("GET  /entries/{entryID}/{$}", entryHandler(c))
	mux.Handle("POST /entries/{entryID}/{$}", entryHandler(c))
	mux.Handle("GET  /entries/{entryID}/runs/{runID}/{$}", runHandler(c))
	mux.Handle("POST /entries/{entryID}/runs/{runID}/{$}", runHandler(c))
	mux.Handle("GET  /hooks/{hookID}/{$}", hookHandler(c))
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

func redirectTo(w http.ResponseWriter, l string) error {
	w.Header().Set("Location", l)
	w.WriteHeader(http.StatusSeeOther)
	return nil
}

func render(code int, name string, data any, w http.ResponseWriter) error {
	tmplHtml, err := fs.ReadFile(name)
	if err != nil {
		return err
	}
	tmpl, err := template.New("").Funcs(funcsMap).Parse(string(tmplHtml))
	if err != nil {
		return err
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, data); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(code)
	if _, err := w.Write(b.Bytes()); err != nil {
		return err
	}
	return nil
}

type M func(w http.ResponseWriter, r *http.Request) error

func (m M) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := (m)(w, r); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type indexData struct {
	Css     template.CSS
	Menu    template.HTML
	JobRuns []cron.JobRun
	Entries []cron.Entry
	Hooks   []cron.Hook
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

type hookData struct {
	Css  template.CSS
	Menu template.HTML
	Hook cron.Hook
}

func indexHandler(c *cron.Cron) http.Handler {
	return M(func(w http.ResponseWriter, r *http.Request) error {
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
			} else if formName == "removeHook" {
				hookID := cron.HookID(r.PostFormValue("hookID"))
				c.RemoveHook(hookID)
			} else if formName == "enableHook" {
				hookID := cron.HookID(r.PostFormValue("hookID"))
				c.EnableHook(hookID)
			} else if formName == "disableHook" {
				hookID := cron.HookID(r.PostFormValue("hookID"))
				c.DisableHook(hookID)
			}
			return redirectTo(w, "/")
		}
		jobRuns := c.RunningJobs()
		entries := c.Entries()
		hooks := c.GetHooks()
		data := indexData{
			JobRuns: jobRuns,
			Entries: entries,
			Hooks:   hooks,
			Css:     template.CSS(getCss()),
			Menu:    template.HTML(getMenu(c)),
		}
		return render(http.StatusOK, "templates/index.gohtml", data, w)
	})
}

func entryHandler(c *cron.Cron) http.Handler {
	return M(func(w http.ResponseWriter, r *http.Request) error {
		entryID := cron.EntryID(r.PathValue("entryID"))
		entry, err := c.Entry(entryID)
		if err != nil {
			return redirectTo(w, "/")
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
			return redirectTo(w, "/entries/"+string(entryID))
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
		return render(http.StatusOK, "templates/entry.gohtml", data, w)
	})
}

func runHandler(c *cron.Cron) http.Handler {
	return M(func(w http.ResponseWriter, r *http.Request) error {
		entryID := cron.EntryID(r.PathValue("entryID"))
		runID := cron.RunID(r.PathValue("runID"))
		entry, entryErr := c.Entry(entryID)
		jobRun, runErr := c.GetJobRun(entryID, runID)
		if entryErr != nil || runErr != nil {
			return redirectTo(w, "/")
		}
		if r.Method == http.MethodPost {
			formName := r.PostFormValue("formName")
			if formName == "cancelRun" {
				_ = c.CancelJobRun(entry.ID, jobRun.RunID)
			}
			return redirectTo(w, "/entries/"+string(entryID)+"/runs/"+string(jobRun.RunID))
		}
		data := runData{
			JobRun: jobRun,
			Entry:  entry,
			Css:    template.CSS(getCss()),
			Menu:   template.HTML(getMenu(c)),
		}
		return render(http.StatusOK, "templates/run.gohtml", data, w)
	})
}

func hookHandler(c *cron.Cron) http.Handler {
	return M(func(w http.ResponseWriter, r *http.Request) error {
		hookID := cron.HookID(r.PathValue("hookID"))
		hook, err := c.GetHook(hookID)
		if err != nil {
			return redirectTo(w, "/")
		}
		if r.Method == http.MethodPost {
			formName := r.PostFormValue("formName")
			if formName == "disableHook" {
			}
			return redirectTo(w, "/hooks/"+string(hookID))
		}
		data := hookData{
			Hook: hook,
			Css:  template.CSS(getCss()),
			Menu: template.HTML(getMenu(c)),
		}
		return render(http.StatusOK, "templates/hook.gohtml", data, w)
	})
}
