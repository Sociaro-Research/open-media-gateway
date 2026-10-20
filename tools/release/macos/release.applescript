-- Opens the local status page. The release process survives closing the page.
on run
    set appPath to POSIX path of (path to me)
    set parentPath to do shell script "/usr/bin/dirname " & quoted form of appPath
    set toolingPath to parentPath & "/Source/"
    try
        do shell script "/bin/mkdir -p " & quoted form of (toolingPath & "logs")
        do shell script "/bin/test -f " & quoted form of (toolingPath & "release_ui.py")
        do shell script "/usr/bin/python3 " & quoted form of (toolingPath & "release_ui.py") & " --launch"
    on error errorMessage
        display dialog "Could not open the checks and deployment window." & return & return & errorMessage with title "Sociaro" buttons {"Close"} default button "Close" with icon caution
    end try
end run
