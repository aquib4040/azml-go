package ext

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"azml-go/config"
	"azml-go/helper/telegram"
	"azml-go/types"
)

var (
	BotStartTime = time.Now()
)

func SetBotStartTime(t time.Time) {
	BotStartTime = t
}

type TaskManager struct {
	tasks map[string]types.DownloadTask
	mu    sync.RWMutex
}

var (
	GlobalTaskManager *TaskManager
	taskOnce          sync.Once

	queuedDL    = make(map[string]chan struct{})
	queuedUP    = make(map[string]chan struct{})
	nonQueuedDL = make(map[string]struct{})
	nonQueuedUP = make(map[string]struct{})
	queueLock   sync.Mutex
)

func GetTaskManager() *TaskManager {
	taskOnce.Do(func() {
		GlobalTaskManager = &TaskManager{
			tasks: make(map[string]types.DownloadTask),
		}
	})
	return GlobalTaskManager
}

func (m *TaskManager) AddTask(t types.DownloadTask) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasks[t.GID()] = t
}

func (m *TaskManager) RemoveTask(gid string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tasks, gid)
}

func (m *TaskManager) GetTask(gid string) types.DownloadTask {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tasks[gid]
}

func (m *TaskManager) GetAllTasks() []types.DownloadTask {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var list []types.DownloadTask
	for _, t := range m.tasks {
		list = append(list, t)
	}
	return list
}

func (m *TaskManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.tasks)
}

func (m *TaskManager) CountUserTasks(userID int64) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, t := range m.tasks {
		if t.UserID() == userID {
			count++
		}
	}
	return count
}

func (m *TaskManager) GetTotalPages() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	totalTasks := len(m.tasks)
	if totalTasks == 0 {
		return 0
	}
	cfg := config.Get()
	limit := cfg.StatusLimit
	if limit <= 0 {
		limit = 4
	}
	return (totalTasks + limit - 1) / limit
}

func (m *TaskManager) CancelAll(statusFilter string) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	cancelled := 0
	for gid, t := range m.tasks {
		if statusFilter == "all" || strings.EqualFold(string(t.Status()), statusFilter) {
			_ = t.Cancel()
			delete(m.tasks, gid)
			cancelled++
		}
	}
	return cancelled
}

func (m *TaskManager) BuildStatusMessage(page int) (string, *telegram.ButtonMaker) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tasks := make([]types.DownloadTask, 0, len(m.tasks))
	for _, t := range m.tasks {
		tasks = append(tasks, t)
	}

	totalTasks := len(tasks)
	if totalTasks == 0 {
		mem := GetMemoryStats()
		uptime := TimeSinceFormatted(BotStartTime)
		noTaskMsg := fmt.Sprintf("<i>No Active Tasks!</i>\n\n"+
			"⌬ <b><i>Bot Stats</i></b>\n"+
			"┠ <b>RAM:</b> <code>%.1f%%</code> | <b>App RAM:</b> <code>%.1f MB</code>\n"+
			"┠ <b>Free:</b> <code>%s</code>\n"+
			"┖ <b>UPTIME:</b> <code>%s</code>",
			mem.OSPercent, mem.AppRAMMB, mem.OSFree, uptime)
		btn := telegram.NewButtonMaker()
		btn.IButton("🔄 Refresh", "status ref 1")
		btn.IButton("❌ Close", "status close")
		return noTaskMsg, btn
	}

	// Deterministic sort by task start time so pages stay stable across edits
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].StartTime().Before(tasks[j].StartTime())
	})

	cfg := config.Get()
	limit := cfg.StatusLimit
	if limit <= 0 {
		limit = 4
	}

	totalPages := (totalTasks + limit - 1) / limit
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}

	start := (page - 1) * limit
	end := start + limit
	if end > totalTasks {
		end = totalTasks
	}

	var totalDL float64
	var totalUL float64
	for _, t := range tasks {
		spd := t.Speed()
		if t.Status() == types.StatusUploading {
			totalUL += spd
		} else {
			totalDL += spd
		}
	}

	var sb strings.Builder
	cancelCmd := "cancel" + cfg.CmdSuffix

	for i := start; i < end; i++ {
		t := tasks[i]
		pct := t.Progress()
		bar := makeProgressBar(pct)
		elapsed := TimeSinceFormatted(t.StartTime())

		sb.WriteString(fmt.Sprintf("<b><i>%s</i></b>\n", SanitizeFilename(t.Name())))
		sb.WriteString(fmt.Sprintf("┃ %s <code>%.1f%%</code>\n", bar, pct))
		sb.WriteString(fmt.Sprintf("┠ <b>Processed:</b> <code>%s</code> of <code>%s</code>\n", GetReadableFileSize(t.DownloadedBytes()), GetReadableFileSize(t.Size())))
		sb.WriteString(fmt.Sprintf("┠ <b>Status:</b> %s | <b>ETA:</b> <code>%s</code>\n", t.Status(), t.ETA()))
		sb.WriteString(fmt.Sprintf("┠ <b>Speed:</b> <code>%s/s</code> | <b>Elapsed:</b> <code>%s</code>\n", GetReadableFileSize(int64(t.Speed())), elapsed))
		sb.WriteString(fmt.Sprintf("┠ <b>Engine:</b> <code>%s</code>\n", t.Engine()))
		uID := t.UserID()
		if uID == 0 {
			uID = cfg.OwnerID
		}
		sb.WriteString(fmt.Sprintf("┠ <b>User:</b> <code>User</code> | <b>ID:</b> <code>%d</code>\n", uID))
		sb.WriteString(fmt.Sprintf("┖ <b>Cancel:</b> <code>/%s_%s</code>\n\n", cancelCmd, t.GID()))
	}

	mem := GetMemoryStats()
	uptime := TimeSinceFormatted(BotStartTime)

	sb.WriteString("⌬ <b><i>Bot Stats</i></b>\n")
	sb.WriteString(fmt.Sprintf("┠ <b>Tasks:</b> <code>%d</code>\n", totalTasks))
	sb.WriteString(fmt.Sprintf("┠ <b>RAM:</b> <code>%.1f%%</code> | <b>App RAM:</b> <code>%.1f MB</code>\n", mem.OSPercent, mem.AppRAMMB))
	sb.WriteString(fmt.Sprintf("┠ <b>UPTIME:</b> <code>%s</code>\n", uptime))
	sb.WriteString(fmt.Sprintf("┖ <b>DL:</b> <code>%s/s</code> | <b>UL:</b> <code>%s/s</code>", GetReadableFileSize(int64(totalDL)), GetReadableFileSize(int64(totalUL))))

	btn := telegram.NewButtonMaker()
	if totalPages > 1 {
		prevPage := page - 1
		if prevPage < 1 {
			prevPage = totalPages
		}
		nextPage := page + 1
		if nextPage > totalPages {
			nextPage = 1
		}

		btn.IButton("⫷", fmt.Sprintf("status page %d", prevPage))
		btn.IButton(fmt.Sprintf("ᴘᴀɢᴇs %d/%d", page, totalPages), fmt.Sprintf("status ref %d", page))
		btn.IButton("⫸", fmt.Sprintf("status page %d", nextPage))
		btn.IButton("❌ Close", "status close")
	} else {
		btn.IButton("🔄 Refresh", "status ref 1")
		btn.IButton("❌ Close", "status close")
	}

	return sb.String(), btn
}

func makeProgressBar(pct float64) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	totalBlocks := 10
	filledBlocks := int((pct / 100.0) * float64(totalBlocks))

	var b strings.Builder
	for i := 0; i < filledBlocks; i++ {
		b.WriteString("■")
	}
	for i := filledBlocks; i < totalBlocks; i++ {
		b.WriteString("□")
	}
	return b.String()
}

// QueueTask implements types.DownloadTask for tasks in QueueDL or QueueUp states
type QueueTask struct {
	gid       string
	name      string
	size      int64
	status    types.TaskStatus
	cancelFn  context.CancelFunc
	startTime time.Time
	chatID    int64
	userID    int64
	msgID     int
	mu        sync.RWMutex
}

func NewQueueTask(gid, name string, size int64, status types.TaskStatus, chatID, userID int64, msgID int, cancelFn context.CancelFunc) *QueueTask {
	return &QueueTask{
		gid:       gid,
		name:      name,
		size:      size,
		status:    status,
		cancelFn:  cancelFn,
		startTime: time.Now(),
		chatID:    chatID,
		userID:    userID,
		msgID:     msgID,
	}
}

func (q *QueueTask) GID() string            { return q.gid }
func (q *QueueTask) Name() string           { return q.name }
func (q *QueueTask) Size() int64            { return q.size }
func (q *QueueTask) DownloadedBytes() int64 { return 0 }
func (q *QueueTask) Speed() float64         { return 0 }
func (q *QueueTask) ETA() string            { return "-" }
func (q *QueueTask) Progress() float64      { return 0 }
func (q *QueueTask) Status() types.TaskStatus {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.status
}
func (q *QueueTask) Engine() string       { return "Sleep v0" }
func (q *QueueTask) StartTime() time.Time { return q.startTime }
func (q *QueueTask) MessageID() int       { return q.msgID }
func (q *QueueTask) ChatID() int64        { return q.chatID }
func (q *QueueTask) UserID() int64        { return q.userID }
func (q *QueueTask) Cancel() error {
	q.mu.Lock()
	cancel := q.cancelFn
	q.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	ReleaseFromQueue(q.gid)
	return nil
}

// UnifiedTask tracks the entire lifecycle of a leech task across all phases
type UnifiedTask struct {
	gid        string
	name       string
	size       int64
	downloaded int64
	speed      float64
	eta        string
	progress   float64
	status     types.TaskStatus
	engine     string
	startTime  time.Time
	messageID  int
	chatID     int64
	userID     int64
	cancelFn   context.CancelFunc
	mu         sync.RWMutex
}

func NewUnifiedTask(gid, name string, size int64, chatID, userID int64, msgID int, cancelFn context.CancelFunc) *UnifiedTask {
	cleanName := name
	if cleanName != "" {
		cleanName = filepath.Base(cleanName)
	}
	return &UnifiedTask{
		gid:       gid,
		name:      cleanName,
		size:      size,
		status:    types.StatusDownloading,
		engine:    "Aria2c [MaxSpeed 16c]",
		startTime: time.Now(),
		chatID:    chatID,
		userID:    userID,
		messageID: msgID,
		cancelFn:  cancelFn,
		eta:       "-",
	}
}

func (u *UnifiedTask) GID() string { return u.gid }
func (u *UnifiedTask) Name() string {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.name
}
func (u *UnifiedTask) Size() int64 {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.size
}
func (u *UnifiedTask) DownloadedBytes() int64 {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.downloaded
}
func (u *UnifiedTask) Speed() float64 {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.speed
}
func (u *UnifiedTask) ETA() string {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.eta
}
func (u *UnifiedTask) Progress() float64 {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.progress
}
func (u *UnifiedTask) Status() types.TaskStatus {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.status
}
func (u *UnifiedTask) Engine() string {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.engine
}
func (u *UnifiedTask) StartTime() time.Time { return u.startTime }
func (u *UnifiedTask) MessageID() int       { return u.messageID }
func (u *UnifiedTask) ChatID() int64        { return u.chatID }
func (u *UnifiedTask) UserID() int64        { return u.userID }

func (u *UnifiedTask) Cancel() error {
	u.mu.Lock()
	u.status = types.StatusPaused
	cancel := u.cancelFn
	u.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (u *UnifiedTask) SetProgress(downloaded, size int64, speed float64, eta string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.downloaded = downloaded
	if size > 0 {
		u.size = size
	}
	u.speed = speed
	u.eta = eta
	if u.size > 0 {
		p := (float64(u.downloaded) / float64(u.size)) * 100
		if p > 100 {
			p = 100
		}
		u.progress = p
	}
}

func (u *UnifiedTask) SetStatus(status types.TaskStatus, engine string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.status = status
	if engine != "" {
		u.engine = engine
	}
}

func (u *UnifiedTask) SetEngine(engine string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if engine != "" {
		u.engine = engine
	}
}

func (u *UnifiedTask) SetName(name string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	clean := filepath.Base(name)
	if clean != "" && clean != "." && clean != "/" {
		u.name = clean
	}
}

func (u *UnifiedTask) SetSize(size int64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if size > 0 {
		u.size = size
	}
}

// AcquireDownloadQueue checks whether a task needs to be queued for downloading.
// If limits require queuing, it registers a QueueTask (status QueueDL, engine Sleep v0)
// into GlobalTaskManager and blocks until released or context cancelled.
func AcquireDownloadQueue(ctx context.Context, uid, name string, size int64, chatID, userID int64, msgID int, cancelFn context.CancelFunc) (bool, error) {
	cfg := config.Get()
	allLimit := cfg.QueueAll
	dlLimit := cfg.QueueDownload

	var waitChan chan struct{}
	queued := false

	queueLock.Lock()
	dl := len(nonQueuedDL)
	up := len(nonQueuedUP)

	if (allLimit > 0 && dl+up >= allLimit && (dlLimit == 0 || dl >= dlLimit)) || (dlLimit > 0 && dl >= dlLimit) {
		queued = true
		waitChan = make(chan struct{})
		queuedDL[uid] = waitChan
	} else {
		nonQueuedDL[uid] = struct{}{}
	}
	queueLock.Unlock()

	if queued {
		log.Printf("[Queue] Task added to QueueDL: %s (UID: %s)", name, uid)
		qTask := NewQueueTask(uid, name, size, types.StatusQueuedDL, chatID, userID, msgID, cancelFn)
		GetTaskManager().AddTask(qTask)

		select {
		case <-waitChan:
			log.Printf("[Queue] Task woken up from QueueDL: %s (UID: %s)", name, uid)
			GetTaskManager().RemoveTask(uid)
			return true, nil
		case <-ctx.Done():
			queueLock.Lock()
			delete(queuedDL, uid)
			queueLock.Unlock()
			GetTaskManager().RemoveTask(uid)
			StartFromQueued()
			return true, ctx.Err()
		}
	}

	return false, nil
}

// ReleaseDownload removes a task from nonQueuedDL and signals the next queued task.
func ReleaseDownload(uid string) {
	queueLock.Lock()
	delete(nonQueuedDL, uid)
	delete(queuedDL, uid)
	queueLock.Unlock()

	StartFromQueued()
}

// AcquireUploadQueue checks whether a task needs to be queued for uploading.
// If limits require queuing, it registers a QueueTask (status QueueUp, engine Sleep v0)
// and blocks until released or context cancelled.
func AcquireUploadQueue(ctx context.Context, uid, name string, size int64, chatID, userID int64, msgID int, cancelFn context.CancelFunc) (bool, error) {
	cfg := config.Get()
	allLimit := cfg.QueueAll
	upLimit := cfg.QueueUpload

	var waitChan chan struct{}
	queued := false

	queueLock.Lock()
	dl := len(nonQueuedDL)
	up := len(nonQueuedUP)

	if (allLimit > 0 && dl+up >= allLimit && (upLimit == 0 || up >= upLimit)) || (upLimit > 0 && up >= upLimit) {
		queued = true
		waitChan = make(chan struct{})
		queuedUP[uid] = waitChan
	} else {
		nonQueuedUP[uid] = struct{}{}
	}
	queueLock.Unlock()

	if queued {
		log.Printf("[Queue] Task added to QueueUp: %s (UID: %s)", name, uid)
		qTask := NewQueueTask(uid, name, size, types.StatusQueuedUP, chatID, userID, msgID, cancelFn)
		GetTaskManager().AddTask(qTask)

		select {
		case <-waitChan:
			log.Printf("[Queue] Task woken up from QueueUp: %s (UID: %s)", name, uid)
			GetTaskManager().RemoveTask(uid)
			return true, nil
		case <-ctx.Done():
			queueLock.Lock()
			delete(queuedUP, uid)
			queueLock.Unlock()
			GetTaskManager().RemoveTask(uid)
			StartFromQueued()
			return true, ctx.Err()
		}
	}

	return false, nil
}

// ReleaseUpload removes a task from nonQueuedUP and signals the next queued task.
func ReleaseUpload(uid string) {
	queueLock.Lock()
	delete(nonQueuedUP, uid)
	delete(queuedUP, uid)
	queueLock.Unlock()

	StartFromQueued()
}

// ReleaseFromQueue releases a task if it was in either queue.
func ReleaseFromQueue(uid string) {
	queueLock.Lock()
	if ch, ok := queuedDL[uid]; ok {
		delete(queuedDL, uid)
		close(ch)
	}
	if ch, ok := queuedUP[uid]; ok {
		delete(queuedUP, uid)
		close(ch)
	}
	delete(nonQueuedDL, uid)
	delete(nonQueuedUP, uid)
	queueLock.Unlock()

	StartFromQueued()
}

// StartFromQueued inspects queue limits and awakens waiting tasks exactly like Python AZML.
func StartFromQueued() {
	queueLock.Lock()
	defer queueLock.Unlock()

	cfg := config.Get()
	allLimit := cfg.QueueAll
	dlLimit := cfg.QueueDownload
	upLimit := cfg.QueueUpload

	dl := len(nonQueuedDL)
	up := len(nonQueuedUP)

	if allLimit > 0 {
		all := dl + up
		if all < allLimit {
			fTasks := allLimit - all
			// Priority to uploads first (same as Python AZML)
			if len(queuedUP) > 0 && (upLimit == 0 || up < upLimit) {
				for uid, ch := range queuedUP {
					delete(queuedUP, uid)
					nonQueuedUP[uid] = struct{}{}
					close(ch)
					up++
					fTasks--
					if fTasks == 0 || (upLimit > 0 && up >= upLimit) {
						break
					}
				}
			}
			// Then downloads
			if len(queuedDL) > 0 && (dlLimit == 0 || dl < dlLimit) && fTasks > 0 {
				for uid, ch := range queuedDL {
					delete(queuedDL, uid)
					nonQueuedDL[uid] = struct{}{}
					close(ch)
					dl++
					fTasks--
					if (dlLimit > 0 && dl >= dlLimit) || fTasks == 0 {
						break
					}
				}
			}
		}
		return
	}

	// No allLimit: independent upload queue
	if upLimit > 0 {
		if len(queuedUP) > 0 && up < upLimit {
			fTasks := upLimit - up
			for uid, ch := range queuedUP {
				delete(queuedUP, uid)
				nonQueuedUP[uid] = struct{}{}
				close(ch)
				up++
				fTasks--
				if fTasks == 0 {
					break
				}
			}
		}
	} else {
		for uid, ch := range queuedUP {
			delete(queuedUP, uid)
			nonQueuedUP[uid] = struct{}{}
			close(ch)
		}
	}

	// Independent download queue
	if dlLimit > 0 {
		if len(queuedDL) > 0 && dl < dlLimit {
			fTasks := dlLimit - dl
			for uid, ch := range queuedDL {
				delete(queuedDL, uid)
				nonQueuedDL[uid] = struct{}{}
				close(ch)
				dl++
				fTasks--
				if fTasks == 0 {
					break
				}
			}
		}
	} else {
		for uid, ch := range queuedDL {
			delete(queuedDL, uid)
			nonQueuedDL[uid] = struct{}{}
			close(ch)
		}
	}
}
