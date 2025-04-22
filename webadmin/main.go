package webadmin

import (
	"bytes"
	"embed"
	"github.com/alaingilbert/cron"
	"github.com/alaingilbert/cron/internal/utils"
	"html/template"
	"net/http"
	"slices"
	"strings"
	"time"
)

//go:embed templates/*
var fs embed.FS

//go:embed css/style.css
var style string

func GetMux(c *cron.Cron) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET  /{$}", indexHandler(c))
	mux.Handle("POST /{$}", indexHandler(c))
	mux.Handle("POST /cleanup-now/{$}", cleanupNowHandler(c))
	mux.Handle("GET  /completed/{$}", completedHandler(c))
	mux.Handle("GET  /entries/{entryID}/{$}", entryHandler(c))
	mux.Handle("POST /entries/{entryID}/{$}", entryHandler(c))
	mux.Handle("GET  /entries/{entryID}/runs/{runID}/{$}", runHandler(c))
	mux.Handle("POST /entries/{entryID}/runs/{runID}/{$}", runHandler(c))
	mux.Handle("GET  /hooks/{hookID}/{$}", hookHandler(c))
	mux.Handle("POST /hooks/{hookID}/{$}", hookHandler(c))
	return mux
}

var funcsMap = template.FuncMap{
	"FmtDate":  func(t time.Time) string { return t.Format(time.DateTime) },
	"ShortDur": func(t time.Time) string { return utils.ShortDur(t) },
	"Nl2br":    func(v string) string { return strings.ReplaceAll(v, "\n", "<br />") },
	"Safe":     func(v string) template.HTML { return template.HTML(v) },
}

func redirectTo(w http.ResponseWriter, l string) error {
	w.Header().Set("Location", l)
	w.WriteHeader(http.StatusSeeOther)
	return nil
}

func render(code int, name string, data any, w http.ResponseWriter) error {
	files := []string{"templates/base.gohtml", name}
	tmpl := template.New("").Funcs(funcsMap)
	for _, fileName := range files {
		fileContent, err := fs.ReadFile(fileName)
		if err != nil {
			return err
		}
		tmpl, err = tmpl.Parse(string(fileContent))
		if err != nil {
			return err
		}
	}
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "base", data); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(code)
	_, err := w.Write(b.Bytes())
	return err
}

type M func(w http.ResponseWriter, r *http.Request) error

func (m M) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := (m)(w, r); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type commonData struct {
	Css       template.CSS
	Now       time.Time
	CleanupTS time.Time
}

func (d *commonData) setCommonData(c *cron.Cron) {
	d.CleanupTS = c.GetCleanupTS()
	d.Now = time.Now()
	d.Css = template.CSS(style)
}

type indexData struct {
	commonData
	JobRuns []cron.JobRun
	Entries []cron.Entry
	Hooks   []cron.Hook
}

type completedData struct {
	commonData
	CompletedJobRuns []cron.JobRun
}

type entryData struct {
	commonData
	Entry            cron.Entry
	JobRuns          []cron.JobRun
	CompletedJobRuns []cron.JobRun
}

type runData struct {
	commonData
	JobRun cron.JobRun
	Entry  cron.Entry
}

type hookData struct {
	commonData
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
		}
		data.setCommonData(c)
		return render(http.StatusOK, "templates/index.gohtml", data, w)
	})
}

func cleanupNowHandler(c *cron.Cron) http.Handler {
	return M(func(w http.ResponseWriter, r *http.Request) error {
		c.CleanupNow()
		return redirectTo(w, r.Referer())
	})
}

func completedHandler(c *cron.Cron) http.Handler {
	return M(func(w http.ResponseWriter, r *http.Request) error {
		completedJobRuns := c.CompletedJobs()
		slices.Reverse(completedJobRuns)
		data := completedData{
			CompletedJobRuns: completedJobRuns,
		}
		data.setCommonData(c)
		return render(http.StatusOK, "templates/completed.gohtml", data, w)
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
		}
		data.setCommonData(c)
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
		}
		data.setCommonData(c)
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
				c.DisableHook(hookID)
			} else if formName == "enableHook" {
				c.EnableHook(hookID)
			} else if formName == "updateLabel" {
				c.SetHookLabel(hookID, r.PostFormValue("label"))
			} else if formName == "removeHook" {
				c.RemoveHook(hookID)
				return redirectTo(w, "/")
			}
			return redirectTo(w, "/hooks/"+string(hookID))
		}
		data := hookData{
			Hook: hook,
		}
		data.setCommonData(c)
		return render(http.StatusOK, "templates/hook.gohtml", data, w)
	})
}
