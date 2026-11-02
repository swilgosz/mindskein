package main

// upgradeTarget is the installed binary upgrade replaces. Tests point it at a
// temporary file so that no run of the suite can overwrite a real install.
var upgradeTarget = binaryPath
