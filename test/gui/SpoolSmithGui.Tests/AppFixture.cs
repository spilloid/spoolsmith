using System;
using System.Diagnostics;
using System.IO;
using FlaUI.Core;
using FlaUI.Core.AutomationElements;
using FlaUI.UIA2;

namespace SpoolSmithGui.Tests;

/// <summary>
/// Launches the real spoolsmith-gui.exe once per test class and attaches to
/// it with UIA2 (not UIA3: UIA3 has known compatibility bugs against raw
/// Win32/WinForms-style controls, which is exactly what the walk toolkit
/// creates). Each STA test owns its automation object and application lifetime
/// so COM objects and window state are not carried between test threads.
/// </summary>
public sealed class AppFixture : IDisposable
{
    public Application App { get; }
    public UIA2Automation Automation { get; }
    public Window MainWindow { get; }
    public string RepoRoot { get; }
    public string CliExePath { get; }

    /// <summary>
    /// Tests launch without the startup scan so they start deterministically.
    /// Screenshot capture opts back in, because scanning the network the PC is
    /// already on is the behaviour being shown.
    /// </summary>
    public AppFixture(bool autoScan = false, string? profilesDirectory = null)
    {
        RepoRoot = FindRepoRoot();
        var exePath = Path.Combine(RepoRoot, "dist", "spoolsmith-gui.exe");
        var fromEnv = Environment.GetEnvironmentVariable("SPOOLSMITH_GUI_EXE");
        if (!string.IsNullOrWhiteSpace(fromEnv))
        {
            exePath = fromEnv;
        }
        if (!File.Exists(exePath))
        {
            throw new FileNotFoundException(
                $"spoolsmith-gui.exe not found at '{exePath}'. Build it first " +
                "(go build -o dist/spoolsmith-gui.exe ./cmd/spoolsmith-gui) or set " +
                "the SPOOLSMITH_GUI_EXE environment variable to its path.",
                exePath);
        }

        // Only the Intune wizard test needs the CLI binary; resolved here (not
        // required to exist) so every other test keeps working without it.
        CliExePath = Path.Combine(RepoRoot, "dist", "spoolsmith.exe");
        var cliFromEnv = Environment.GetEnvironmentVariable("SPOOLSMITH_CLI_EXE");
        if (!string.IsNullOrWhiteSpace(cliFromEnv))
        {
            CliExePath = cliFromEnv;
        }

        var startInfo = new ProcessStartInfo(exePath)
        {
            UseShellExecute = false,
            WorkingDirectory = RepoRoot,
        };
        // Tests start deterministically without probing the operator's Wi-Fi.
        // The discovery test explicitly scans loopback unless opted in locally.
        if (!autoScan)
        {
            startInfo.Environment["SPOOLSMITH_GUI_NO_AUTOSCAN"] = "1";
        }
        if (profilesDirectory != null) startInfo.Environment["SPOOLSMITH_PROFILES_DIR"] = profilesDirectory;
        App = Application.Launch(startInfo);
        Automation = new UIA2Automation();
        MainWindow = GetMainWindowWithRetry();
        MoveToKnownPosition();
    }

    /// <summary>
    /// Puts the window somewhere every test can see all of it.
    ///
    /// Two things otherwise push part of it off the display, and UI Automation
    /// then correctly reports those controls as IsOffscreen — which reads as a
    /// missing button rather than a placement artifact. Observed directly: a
    /// button lookup failed late in a full run and passed when run alone.
    ///
    /// First, Windows cascades each successive new window down and to the
    /// right, and a full run launches the app once per test. Second, the
    /// default 980x740 window is simply taller than a small desktop — a CI
    /// runner at 1024x768 among them — so it is shrunk to fit when it has to
    /// be. Walk enforces the app's own minimum size, so this never shrinks
    /// past a layout the app considers valid.
    /// </summary>
    private void MoveToKnownPosition()
    {
        try
        {
            var hwnd = MainWindow.Properties.NativeWindowHandle.Value;
            var workArea = System.Windows.Forms.Screen.PrimaryScreen?.WorkingArea
                ?? new System.Drawing.Rectangle(0, 0, 1024, 768);
            var bounds = MainWindow.BoundingRectangle;
            var fits = bounds.Width > 0 && bounds.Height > 0
                && bounds.Width <= workArea.Width && bounds.Height <= workArea.Height;
            if (fits)
            {
                // Move only. Sizing from BoundingRectangle is not safe here: it
                // can be read before the window has settled, and this process is
                // not per-monitor DPI aware while the app is, so on a scaled
                // display those numbers are not the window's real pixel size.
                // Passing them back shrank the window and clipped its bottom
                // row of buttons, which surfaced as a button that could not be
                // found at all.
                SetWindowPos(hwnd, IntPtr.Zero, workArea.X, workArea.Y, 0, 0,
                    SWP_NOSIZE | SWP_NOZORDER | SWP_NOACTIVATE);
                return;
            }
            // Too tall or wide for this desktop — a 1024x768 CI runner, say.
            // Fall back to the app's own minimum, scaled the way TruncationTests
            // already does it, rather than to a size derived from a rectangle
            // that may be in different units.
            var scale = GetDpiForWindow(hwnd) / 96.0;
            SetWindowPos(hwnd, IntPtr.Zero, workArea.X, workArea.Y,
                Math.Min((int)Math.Round(960 * scale), workArea.Width),
                Math.Min((int)Math.Round(640 * scale), workArea.Height),
                SWP_NOZORDER | SWP_NOACTIVATE);
        }
        catch
        {
            // Placement is a convenience; a failure here must not mask the
            // actual assertion the test is making.
        }
    }

    private const uint SWP_NOSIZE = 0x0001;
    private const uint SWP_NOZORDER = 0x0004;
    private const uint SWP_NOACTIVATE = 0x0010;

    [System.Runtime.InteropServices.DllImport("user32.dll")]
    private static extern uint GetDpiForWindow(IntPtr hwnd);

    [System.Runtime.InteropServices.DllImport("user32.dll", SetLastError = true)]
    [return: System.Runtime.InteropServices.MarshalAs(System.Runtime.InteropServices.UnmanagedType.Bool)]
    private static extern bool SetWindowPos(IntPtr hwnd, IntPtr insertAfter, int x, int y, int width, int height, uint flags);

    /// <summary>
    /// UIA2's AutomationElement.FromHandle can throw a transient COMException
    /// (E_FAIL from UiaNodeFromHandle) when called right as a window is being
    /// created — a documented UI-Automation startup race, not a real failure.
    /// Retrying a few times is the standard workaround.
    /// </summary>
    private Window GetMainWindowWithRetry()
    {
        Exception? last = null;
        for (var attempt = 0; attempt < 5; attempt++)
        {
            try
            {
                var window = App.GetMainWindow(Automation, TimeSpan.FromSeconds(15))
                    ?? throw new TimeoutException("SpoolSmith's main window never appeared.");
                // GetMainWindow can hand back the window before it is fully
                // realized, when it still reports an empty title. Observed
                // intermittently: a run failed asserting the title was
                // "SpoolSmith" and saw "" instead. Wait for the real title
                // rather than letting the race surface as a content assertion.
                var ready = DateTime.UtcNow.AddSeconds(10);
                while (string.IsNullOrEmpty(window.Title) && DateTime.UtcNow < ready)
                {
                    System.Threading.Thread.Sleep(100);
                }
                return window;
            }
            catch (System.Runtime.InteropServices.COMException ex)
            {
                last = ex;
                System.Threading.Thread.Sleep(500);
            }
        }
        throw new InvalidOperationException("Could not attach to SpoolSmith's main window after retrying.", last);
    }

    /// <summary>The sidebar's pages, in order, as their list items are named.</summary>
    public static readonly string[] Pages =
        { "This PC", "Add a printer", "Review and apply", "Intune package", "Inspect", "Driver catalog", "Action log" };

    /// <summary>
    /// Opens a page from the sidebar and waits until its content is visible.
    ///
    /// The sidebar is an owner-drawn native list whose items keep their text,
    /// so UIA exposes each one as a named ListItem. Uses a real click, like an
    /// operator: the app navigates on the list's own selection notification.
    /// </summary>
    public AutomationElement GoTo(string page)
    {
        var marker = page switch
        {
            "This PC" => "thispc-list",
            "Add a printer" => "discover-cidr",
            "Review and apply" => "mutate-output",
            "Intune package" => "Build an Intune printer app...",
            "Inspect" => "inspect-target",
            "Driver catalog" => "catalog-output",
            "Action log" => "log-output",
            _ => throw new ArgumentException("Unknown page", nameof(page)),
        };
        var nav = MainWindow.FindFirstDescendant(cf => cf.ByName("navigation").And(cf.ByControlType(FlaUI.Core.Definitions.ControlType.List)))
            ?? throw new InvalidOperationException("Sidebar navigation not found.");
        var item = nav.FindFirstDescendant(cf => cf.ByControlType(FlaUI.Core.Definitions.ControlType.ListItem).And(cf.ByName(page)))
            ?? throw new InvalidOperationException($"Sidebar item '{page}' not found.");
        MainWindow.SetForeground();
        item.Click();
        var deadline = DateTime.UtcNow.AddSeconds(5);
        while (DateTime.UtcNow < deadline)
        {
            var content = MainWindow.FindFirstDescendant(cf => cf.ByName(marker));
            if (content != null && !content.IsOffscreen) return item;
            // Retry harmless navigation if a launch/focus transition consumed the click.
            MainWindow.SetForeground();
            item.Click();
            System.Threading.Thread.Sleep(100);
        }
        throw new TimeoutException($"Page '{page}' did not show its content after clicking.");
    }

    /// <summary>Absolute path to a file under the repo's fixtures/ directory.</summary>
    public string FixturePath(string name) => Path.Combine(RepoRoot, "fixtures", name);

    internal static string FindRepoRoot()
    {
        var dir = new DirectoryInfo(AppContext.BaseDirectory);
        while (dir != null && !File.Exists(Path.Combine(dir.FullName, "go.mod")))
        {
            dir = dir.Parent;
        }
        if (dir == null)
        {
            throw new InvalidOperationException(
                "Could not locate the spoolsmith repo root (no go.mod found above the test output directory).");
        }
        return dir.FullName;
    }

    public void Dispose()
    {
        try
        {
            App.Close();
        }
        catch
        {
            // Best-effort: the app may already be gone if a test crashed it.
        }
        Automation.Dispose();
        App.Dispose();
    }
}
