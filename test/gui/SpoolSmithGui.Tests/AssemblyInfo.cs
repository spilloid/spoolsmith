using Xunit;

// GUI-automation tests drive one real desktop session (foreground window,
// single mouse/keyboard input queue). Running test classes in parallel means
// two AppFixtures launch spoolsmith-gui.exe at overlapping times, which
// produced real, observed COMException(E_FAIL) races attaching UI Automation
// to a window that's still being created. Running serially is standard
// practice for this class of test and removed the flake in practice here.
[assembly: CollectionBehavior(DisableTestParallelization = true)]
