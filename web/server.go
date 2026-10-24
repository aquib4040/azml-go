package web

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"unicode"

	"azml-go/config"
	"azml-go/helper/downloader"
)

func StartWebServer() {
	cfg := config.Get()
	port := cfg.BaseURLPort
	if pStr := os.Getenv("PORT"); pStr != "" {
		if p, err := strconv.Atoi(strings.TrimSpace(pStr)); err == nil && p > 0 {
			port = p
		}
	}
	if port <= 0 {
		port = 23434
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/app/files/", handleFiles)

	addr := fmt.Sprintf(":%d", port)
	log.Printf("[WEB SERVER] Starting Base URL web selector server on port %d...", port)
	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil && err != http.ErrServerClosed {
			log.Printf("[WEB SERVER] Server error: %v", err)
		}
	}()
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(homepageHTML))
}

func handleFiles(w http.ResponseWriter, r *http.Request) {
	// Path is /app/files/{id}
	id := strings.TrimPrefix(r.URL.Path, "/app/files/")
	id = strings.TrimSpace(id)
	if id == "" {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	// Compute PIN code: first 4 digits of ID
	pinCode := ""
	for _, ch := range id {
		if unicode.IsDigit(ch) {
			pinCode += string(ch)
			if len(pinCode) == 4 {
				break
			}
		}
	}
	if len(pinCode) < 4 {
		pinCode = "0000"
	}

	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		var selectedIDs []string
		for key, vals := range r.PostForm {
			if strings.HasPrefix(key, "filenode_") && len(vals) > 0 && vals[0] == "on" {
				nodeID := strings.TrimPrefix(key, "filenode_")
				selectedIDs = append(selectedIDs, nodeID)
			}
		}

		if GDriveStore.GetData(id) != nil {
			GDriveStore.SaveSelection(id, selectedIDs)
			log.Printf("[WEB GDRIVE] GID=%s saved %d selected files", id, len(selectedIDs))
		} else if MegaStore.GetData(id) != nil {
			MegaStore.SaveSelection(id, selectedIDs)
			log.Printf("[WEB MEGA] GID=%s saved %d selected files", id, len(selectedIDs))
		} else {
			// Aria2c torrent
			aria := downloader.GetAria2Client()
			if aria != nil {
				resumeList := strings.Join(selectedIDs, ",")
				_ = aria.ChangeOption(id, map[string]interface{}{"select-file": resumeList})
				log.Printf("[WEB ARIA2] GID=%s updated select-file: %s", id, resumeList)
			}
		}

		http.Redirect(w, r, fmt.Sprintf("/app/files/%s?pin_code=%s", id, pinCode), http.StatusSeeOther)
		return
	}

	// Check PIN code from query param
	userPin := r.URL.Query().Get("pin_code")
	if userPin == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		codePage := strings.ReplaceAll(codePageHTML, "{form_url}", fmt.Sprintf("/app/files/%s", id))
		_, _ = w.Write([]byte(codePage))
		return
	}

	if userPin != pinCode {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`<h1 style="color:red;text-align:center;margin-top:20vh;font-family:sans-serif;">× Incorrect Security PIN Code</h1>`))
		return
	}

	// Build content tree
	contentHTML := ""
	if gdFiles := GDriveStore.GetData(id); gdFiles != nil {
		contentHTML = BuildGDriveHTMLTree(gdFiles)
	} else if megaFiles := MegaStore.GetData(id); megaFiles != nil {
		contentHTML = BuildMegaHTMLTree(megaFiles)
	} else {
		// Try Aria2c
		aria := downloader.GetAria2Client()
		if aria != nil {
			status, err := aria.TellStatus(id)
			if err == nil && len(status.Files) > 0 {
				contentHTML = BuildTorrentHTMLTree(status.Files)
			}
		}
	}

	if contentHTML == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<h1 style="color:#a855f7;text-align:center;margin-top:20vh;font-family:sans-serif;">⌬ No files found or session expired for ID: ` + id + `</h1>`))
		return
	}

	formURL := fmt.Sprintf("/app/files/%s?pin_code=%s", id, pinCode)
	pageRendered := strings.ReplaceAll(pageHTML, "{My_content}", contentHTML)
	pageRendered = strings.ReplaceAll(pageRendered, "{form_url}", formURL)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(pageRendered))
}

const homepageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>AZML-Go | Next Gen Leech Engine</title>
    <link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.5.1/css/all.min.css">
    <link href="https://fonts.googleapis.com/css2?family=Exo:wght@300;400;600;700&display=swap" rel="stylesheet">
    <style>
        body {
            font-family: 'Exo', sans-serif !important;
            background-color: #0a0f1c;
            color: #ffffff;
            margin: 0;
            padding: 0;
            display: flex;
            align-items: center;
            justify-content: center;
            min-height: 100vh;
        }
        .container {
            text-align: center;
            padding: 2.5rem;
            max-width: 500px;
            background: rgba(16, 24, 48, 0.7);
            border-radius: 20px;
            box-shadow: 0 0 30px rgba(0, 183, 255, 0.2);
            border: 1px solid rgba(0, 183, 255, 0.3);
            backdrop-filter: blur(15px);
        }
        .logo {
            width: 100px;
            height: 100px;
            border-radius: 50%;
            border: 2px solid #00b7ff;
            box-shadow: 0 0 20px rgba(0, 183, 255, 0.4);
            margin-bottom: 1rem;
        }
        .version {
            display: inline-block;
            background: rgba(0, 183, 255, 0.2);
            color: #00b7ff;
            padding: 4px 12px;
            border-radius: 20px;
            font-size: 0.8rem;
            font-weight: bold;
            margin-bottom: 0.5rem;
        }
        h1 {
            font-size: 2.5rem;
            margin: 0.5rem 0;
            letter-spacing: 2px;
            color: #00b7ff;
        }
        .subtitle {
            color: #94a3b8;
            font-size: 1rem;
            margin-bottom: 2rem;
        }
        .links {
            display: flex;
            flex-direction: column;
            gap: 1rem;
        }
        .button {
            display: flex;
            align-items: center;
            justify-content: center;
            gap: 0.75rem;
            padding: 0.9rem;
            background: linear-gradient(45deg, rgba(0, 183, 255, 0.2), rgba(168, 85, 247, 0.2));
            color: #ffffff;
            text-decoration: none;
            border-radius: 12px;
            font-weight: bold;
            transition: all 0.3s ease;
            border: 1px solid rgba(255, 255, 255, 0.1);
        }
        .button:hover {
            transform: translateY(-2px);
            background: linear-gradient(45deg, #00b7ff, #a855f7);
            color: #000;
            box-shadow: 0 5px 20px rgba(0, 183, 255, 0.4);
        }
        .copyright {
            margin-top: 2rem;
            font-size: 0.8rem;
            color: #64748b;
        }
    </style>
</head>
<body>
    <div class="container">
        <img class="logo" src="https://graph.org/file/1a6ad157f55bc42b548df.png" alt="AZML Logo">
        <div><span class="version">AZML-Go Pure Edition</span></div>
        <h1>AZML</h1>
        <p class="subtitle">Next generation high-speed workflow automation powered by Go</p>
        <div class="links">
            <a href="https://t.me/CANON_BOTS" target="_blank" class="button">
                <i class="fab fa-telegram-plane"></i> Telegram Channel
            </a>
            <a href="https://github.com/aquib4040/AZML" target="_blank" class="button">
                <i class="fab fa-github"></i> GitHub Repository
            </a>
        </div>
        <p class="copyright">&copy; 2026 AZML-Go. All rights reserved.</p>
    </div>
</body>
</html>`

const codePageHTML = `<!DOCTYPE html>
<html class="dark" lang="en">
<head>
    <meta charset="utf-8"/>
    <meta content="width=device-width, initial-scale=1.0" name="viewport"/>
    <title>Security Access - AZML Selector</title>
    <script src="https://cdn.tailwindcss.com?plugins=forms,container-queries"></script>
    <link href="https://fonts.googleapis.com/css2?family=Manrope:wght@400;700;800&family=Inter:wght@400;500;600&display=swap" rel="stylesheet"/>
    <link href="https://fonts.googleapis.com/css2?family=Material+Symbols+Outlined:wght,FILL@100..700,0..1&display=swap" rel="stylesheet"/>
    <style>
        body { background-color: #000000; color: #fafafa; }
        .glass-card {
            background: rgba(10, 10, 12, 0.8);
            backdrop-filter: blur(20px);
            border: 1px solid #27272a;
        }
    </style>
</head>
<body class="antialiased min-h-screen flex items-center justify-center p-6 relative font-sans">
    <div class="w-full max-w-lg glass-card rounded-2xl shadow-2xl overflow-hidden text-center p-10 relative">
        <div class="w-16 h-16 mx-auto rounded-full bg-zinc-900 border border-zinc-700 flex items-center justify-center mb-6">
            <span class="material-symbols-outlined text-purple-500 text-3xl">lock</span>
        </div>
        <h1 class="text-2xl font-extrabold text-white mb-2">Authorization Required</h1>
        <p class="text-zinc-400 mb-8 text-sm">Enter the 4-digit security PIN provided in Telegram to access your files.</p>
        <form action="{form_url}">
            <div class="mb-8">
                <input 
                    type="text" 
                    name="pin_code"
                    placeholder="Enter 4-digit PIN"
                    class="w-full bg-black border border-zinc-700 rounded-xl px-6 py-4 text-center text-xl tracking-[0.5em] text-white focus:border-purple-500 focus:outline-none transition-all font-mono"
                    autocomplete="off"
                    maxlength="4"
                    required
                />
            </div>
            <button type="submit" class="w-full bg-purple-600 hover:bg-purple-700 text-white font-bold py-4 rounded-xl transition-all shadow-[0_0_20px_rgba(168,85,247,0.3)]">
                Authenticate
            </button>
        </form>
    </div>
</body>
</html>`

const pageHTML = `<!DOCTYPE html>
<html class="dark" lang="en">
<head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>AZML File Selector</title>
    <link rel="icon" href="https://graph.org/file/1a6ad157f55bc42b548df.png" type="image/jpg">
    <script src="https://code.jquery.com/jquery-3.6.0.min.js"></script>
    <script src="https://cdn.tailwindcss.com?plugins=forms,container-queries"></script>
    <link href="https://fonts.googleapis.com/css2?family=Manrope:wght@400;700;800&family=Inter:wght@400;500;600&display=swap" rel="stylesheet"/>
    <link href="https://fonts.googleapis.com/css2?family=Material+Symbols+Outlined:wght,FILL@100..700,0..1&display=swap" rel="stylesheet"/>
    <style>
        body { background-color: #000000; color: #fafafa; }
        .glass-card {
            background: rgba(10, 10, 12, 0.7);
            backdrop-filter: blur(20px);
            -webkit-backdrop-filter: blur(20px);
            border: 1px solid #27272a;
        }
        .neon-checkbox {
            appearance: none;
            width: 1.2rem;
            height: 1.2rem;
            border: 1px solid #27272a;
            border-radius: 4px;
            background-color: #000000;
            display: inline-grid;
            place-content: center;
            cursor: pointer;
            position: relative;
            transition: all 0.2s ease-in-out;
        }
        .neon-checkbox::before {
            content: "";
            width: 0.6rem;
            height: 0.6rem;
            transform: scale(0);
            transition: 120ms transform ease-in-out;
            box-shadow: inset 1em 1em #a855f7;
            background-color: CanvasText;
            transform-origin: center;
            clip-path: polygon(14% 44%, 0 65%, 50% 100%, 100% 16%, 80% 0%, 43% 62%);
        }
        .neon-checkbox:checked::before {
            transform: scale(1);
        }
        .neon-checkbox:checked {
            border-color: #a855f7;
            box-shadow: 0 0 10px rgba(168, 85, 247, 0.4);
        }
    </style>
</head>
<body class="antialiased min-h-screen p-2 sm:p-4 md:p-8 font-sans relative">
    <div class="max-w-4xl mx-auto flex flex-col gap-3 sm:gap-6">
        <!-- Header -->
        <header class="glass-card rounded-2xl p-3 sm:p-4 flex items-center justify-between shadow-xl">
            <div class="flex items-center gap-3">
                <img class="w-8 h-8 sm:w-10 sm:h-10 rounded-full border border-purple-500/30" src="https://graph.org/file/1a6ad157f55bc42b548df.png" alt="logo" />
                <h1 class="font-bold text-lg sm:text-xl tracking-tight text-white">AZML File Selection</h1>
            </div>
            <div class="flex items-center gap-3 sm:gap-4 text-zinc-400">
                <a href="https://t.me/CANON_BOTS" target="_blank" class="hover:text-purple-400 transition-colors"><span class="material-symbols-outlined text-lg sm:text-2xl">chat</span></a>
            </div>
        </header>

        <!-- Stats Panel -->
        <div id="sticks" class="glass-card rounded-2xl p-3 sm:p-5 flex flex-row justify-around items-center gap-2 sm:gap-4 text-center shadow-lg transition-all z-50">
            <div>
                <span class="text-[10px] sm:text-xs uppercase tracking-wider text-zinc-400">Selected Files</span>
                <h4 class="text-base sm:text-lg font-bold text-purple-400 mt-0.5 sm:mt-1"><span id="checked_files">0</span> / <span id="total_files">0</span></h4>
            </div>
            <div class="w-[1px] h-8 bg-zinc-800"></div>
            <div>
                <span class="text-[10px] sm:text-xs uppercase tracking-wider text-zinc-400">Selected Size</span>
                <h4 class="text-base sm:text-lg font-bold text-purple-400 mt-0.5 sm:mt-1"><span id="checked_size">0</span> / <span id="total_size">0</span></h4>
            </div>
        </div>

        <!-- File List / Selector -->
        <section class="glass-card rounded-2xl p-3 sm:p-6 shadow-2xl relative">
            <form action="{form_url}" onsubmit="return s_validate()" method="POST" class="flex flex-col gap-3 sm:gap-6">
                <div class="flex flex-col gap-1 rounded-xl bg-black/40 border border-zinc-800 p-2 sm:p-4 max-h-[65vh] overflow-y-auto overflow-x-hidden">
                    {My_content}
                </div>
                <button type="submit" class="self-center w-full sm:w-1/2 bg-purple-600 hover:bg-purple-700 text-white font-bold py-3 sm:py-3.5 rounded-xl transition-all shadow-[0_0_20px_rgba(168,85,247,0.2)] text-sm sm:text-base">
                    Confirm Selection
                </button>
            </form>
        </section>
    </div>

    <!-- Scripts -->
    <script>
        function s_validate() {
            if ($("input[type='checkbox'][name^='filenode_']:checked").length == 0) {
                alert("Select at least one file!");
                return false;
            }
        }

        $(document).ready(function () {
            docready();
            
            // Toggle folder expansion
            $('.file-row').click(function(e) {
                if ($(e.target).is('input[type="checkbox"]')) {
                    return;
                }
                var checkbox = $(this).find('input[type="checkbox"]');
                if (checkbox.length && !checkbox.hasClass('folder-checkbox')) {
                    checkbox.prop('checked', !checkbox.prop('checked')).change();
                    return;
                }
                var nextContainer = $(this).next('div');
                if (nextContainer.length) {
                    nextContainer.slideToggle('fast');
                }
            });

            // Checkbox change cascade
            $('input[type="checkbox"]').change(function(e) {
                var isChecked = $(this).prop('checked');
                if ($(this).hasClass('folder-checkbox')) {
                    var folderContainer = $(this).closest('.flex-col');
                    folderContainer.find('input[type="checkbox"]').not(this).prop('checked', isChecked);
                }
                checkingfiles();
                checked_size();
            });
        });

        function docready() {
            checked_size();
            checkingfiles();
            var fileInputs = $("input[type='checkbox'][name^='filenode_']");
            $("#total_files").text(fileInputs.length);
            var total_size = 0;
            fileInputs.each(function () {
                var size = parseFloat($(this).data("size"));
                if (!isNaN(size)) total_size += size;
            });
            $("#total_size").text(humanFileSize(total_size));
        }

        function checked_size() {
            var checked_size = 0;
            var checkedboxes = $("input[type='checkbox'][name^='filenode_']:checked");
            checkedboxes.each(function () {
                var size = parseFloat($(this).data("size"));
                if (!isNaN(size)) checked_size += size;
            });
            $("#checked_size").text(humanFileSize(checked_size));
        }

        function checkingfiles() {
            var checked_files = $("input[type='checkbox'][name^='filenode_']:checked").length;
            $("#checked_files").text(checked_files);
        }

        function humanFileSize(bytes) {
            if (!bytes || bytes <= 0 || isNaN(bytes)) return '0 B';
            var units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
            var i = Math.floor(Math.log(bytes) / Math.log(1024));
            i = Math.min(Math.max(0, i), units.length - 1);
            return (bytes / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 2) + ' ' + units[i];
        }
    </script>
</body>
</html>`
