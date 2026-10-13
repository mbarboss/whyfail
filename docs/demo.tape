# Demo for the README, recorded with VHS (https://github.com/charmbracelet/vhs):
#   vhs docs/demo.tape
# Needs whyfail on PATH, a running Ollama with the default model, ttyd and ffmpeg.
# Answers come from the model, so each recording differs a little.

Output docs/demo.gif

Set Shell bash
Set FontSize 14
Set Width 1400
Set Height 460
Set Padding 16
Set Framerate 12
Set TypingSpeed 25ms
Set Theme "Builtin Dark"

# Setup: an empty repository without a remote, a short prompt, and one request
# so the model is already loaded when recording starts.
Hide
Type "cd $(mktemp -d) && git init -q && export PS1='$ ' PROMPT_COMMAND=''" Enter
Type "echo 'error: warm up' | whyfail >/dev/null 2>&1; clear" Enter
Sleep 20s
Show

Type "whyfail -- git push origin main"
Sleep 400ms
Enter
Sleep 7s

Type "clear" Enter
Type `python3 -c "import reqeusts" 2>&1 | whyfail`
Sleep 400ms
Enter
Sleep 7s

Type "clear" Enter
Type `echo "permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock" | whyfail`
Sleep 400ms
Enter
Sleep 8s
