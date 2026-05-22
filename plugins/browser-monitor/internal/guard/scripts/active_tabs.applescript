-- Emits one line per open browser tab: APPNAME<TAB>URL
--
-- We FIRST ask System Events for the set of currently-running process
-- names, and only `tell` a browser that is actually running. Referencing
-- `application "Brave Browser"` by name when it is NOT installed makes
-- macOS pop a "choose application" locate dialog — gating on the running
-- process set avoids touching uninstalled apps entirely.
--
-- tabChar is bound in the OUTER scope on purpose: inside a
-- `tell application "Google Chrome"` block the bareword `tab` is shadowed
-- by the browser's Tab class, so building an ASCII tab inline yields the
-- literal text "tab". (Ported in spirit from app_mon/browser_guard.)
set tabChar to (ASCII character 9)
set lf to linefeed
set out to ""

tell application "System Events"
	set runningApps to name of (every process whose background only is false)
end tell

on isRunning(appName, runningApps)
	repeat with n in runningApps
		if (n as text) is appName then return true
	end repeat
	return false
end isRunning

-- Chromium-family browsers share the same scripting dictionary.
repeat with appName in {"Google Chrome", "Brave Browser", "Microsoft Edge"}
	set a to (appName as text)
	if isRunning(a, runningApps) then
		try
			tell application a
				repeat with w in windows
					repeat with t in tabs of w
						try
							set u to URL of t
							if u is missing value then set u to ""
							if u is not "" then set out to out & a & tabChar & u & lf
						end try
					end repeat
				end repeat
			end tell
		end try
	end if
end repeat

if isRunning("Safari", runningApps) then
	try
		tell application "Safari"
			repeat with w in windows
				repeat with t in tabs of w
					try
						set u to URL of t
						if u is missing value then set u to ""
						if u is not "" then set out to out & "Safari" & tabChar & u & lf
					end try
				end repeat
			end repeat
		end tell
	end try
end if

return out
