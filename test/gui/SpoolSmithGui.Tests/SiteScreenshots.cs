using System;
using System.IO;
using System.Linq;
using System.Threading;
using FlaUI.Core.AutomationElements;
using Xunit;

namespace SpoolSmithGui.Tests;

// Captures the real running app, never a mockup. Opt in on a Windows desktop.
public sealed class SiteScreenshots
{
    [StaFact]
    public void Capture_site_screenshots()
    {
        if (Environment.GetEnvironmentVariable("SPOOLSMITH_CAPTURE_SITE_SHOTS") != "1") return;
        // Images are published, so every path they show lives in a neutral demo
        // folder rather than under the capturing operator's user profile. The
        // example setup gives the saved-setups capture a real entry to show.
        var repo = AppFixture.FindRepoRoot();
        Directory.CreateDirectory(DemoSetups);
        File.Copy(Path.Combine(repo, "examples", "intune", "accounting.ssb"), Path.Combine(DemoSetups, "accounting.ssb"), overwrite: true);
        File.Copy(Path.Combine(repo, "dist", "spoolsmith.exe"), DemoCli, overwrite: true);
        var images = Path.Combine(repo, "docs", "img");
        Directory.CreateDirectory(images);
        // First, in its own app: opening a printer file lands on the apply
        // sheet. It must close before the next launch, which would otherwise
        // hand its window over to this one.
        CaptureApplySheet(Path.Combine(DemoSetups, "accounting.ssb"), images);
        using var fixture = new AppFixture(profilesDirectory: DemoSetups);
        foreach (var (tab, file) in new[] {
            ("This PC", "gui-this-pc.png"),
            ("Add a printer", "gui-add-printer.png"),
            ("Inspect", "gui-tools.png"),
            ("Intune package", "gui-intune-page.png") })
        {
            fixture.GoTo(tab);
            if (tab == "This PC")
            {
                // Show the inventory, not the "Reading this PC's printers..." state.
                FunctionalTests.WaitUntil(() => FunctionalTests.FindButton(fixture.MainWindow, "Refresh").IsEnabled,
                    "Printer inventory did not finish loading.");
            }
            Thread.Sleep(500);
            fixture.MainWindow.CaptureToFile(Path.Combine(images, file));
        }
        // Before CaptureIntuneWizard, which is written to be the last step
        // in this method: it never closes its own modal dialog, so anything
        // run after it would fight that leftover dialog for the main window.
        CaptureSavedSetups(fixture, images);
        CaptureCopyAllDialog(fixture, images);
        CaptureIntuneWizard(fixture, images);
    }

    private static readonly string DemoRoot = Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData), "SpoolSmith-demo");
    private static readonly string DemoSetups = Path.Combine(DemoRoot, "setups");
    private static readonly string DemoCli = Path.Combine(DemoRoot, "spoolsmith.exe");

    /// <summary>
    /// Saved setups is a dialog opened from Add a printer. Captured with the
    /// repository's example setup folder so the list is never empty.
    /// </summary>
    private static void CaptureSavedSetups(AppFixture fixture, string images)
    {
        fixture.GoTo("Add a printer");
        FunctionalTests.FindButton(fixture.MainWindow, "Open a saved setup...").Invoke();
        Window? dialog = null;
        FunctionalTests.WaitUntil(() => (dialog = fixture.MainWindow.ModalWindows
            .Concat(fixture.App.GetAllTopLevelWindows(fixture.Automation))
            .FirstOrDefault(w => w.Title == "Saved printer setups")) != null,
            "Saved setups did not open.");
        try
        {
            Thread.Sleep(300);
            dialog!.CaptureToFile(Path.Combine(images, "gui-saved-setups.png"));
        }
        finally
        {
            FunctionalTests.FindButton(dialog!, "Close").Invoke();
            FunctionalTests.WaitUntil(() => fixture.MainWindow.IsEnabled, "Saved setups did not close.");
        }
    }

    /// <summary>
    /// Copying several printers opens a set dialog from This PC. The capture
    /// selects every copyable printer; it skips silently on a machine with
    /// fewer than two (a hosted runner usually has only Microsoft's virtual
    /// printers, which SpoolSmith can't copy).
    /// </summary>
    private static void CaptureCopyAllDialog(AppFixture fixture, string images)
    {
        fixture.GoTo("This PC");
        FunctionalTests.WaitUntil(() => FunctionalTests.FindButton(fixture.MainWindow, "Refresh").IsEnabled,
            "Printer inventory did not finish loading.");
        fixture.MainWindow.SetForeground();
        FunctionalTests.Find(fixture.MainWindow, "thispc-list").Focus();
        FlaUI.Core.Input.Keyboard.TypeSimultaneously(FlaUI.Core.WindowsAPI.VirtualKeyShort.CONTROL, FlaUI.Core.WindowsAPI.VirtualKeyShort.KEY_A);
        Thread.Sleep(300);
        var copy = fixture.MainWindow.FindAllDescendants(cf => cf.ByControlType(FlaUI.Core.Definitions.ControlType.Button))
            .Select(b => b.AsButton())
            .FirstOrDefault(b => !b.IsOffscreen && b.IsEnabled && b.Name.StartsWith("Copy ", StringComparison.Ordinal) && b.Name.EndsWith(" printers...", StringComparison.Ordinal));
        if (copy == null) return;
        var title = copy.Name.TrimEnd('.');
        copy.Invoke();
        Window? dialog = null;
        FunctionalTests.WaitUntil(() => (dialog = fixture.MainWindow.ModalWindows
            .Concat(fixture.App.GetAllTopLevelWindows(fixture.Automation))
            .FirstOrDefault(w => w.Title == title)) != null,
            "Copying the selection did not open.");
        try
        {
            FunctionalTests.SetText(FunctionalTests.Find(dialog!, "copy-all-file").AsTextBox(), Path.Combine(DemoRoot, "SpoolSmith-printers.zip"));
            Thread.Sleep(300);
            dialog!.CaptureToFile(Path.Combine(images, "gui-copy-all.png"));
        }
        finally
        {
            FunctionalTests.FindButton(dialog!, "Cancel").Invoke();
            FunctionalTests.WaitUntil(() => fixture.MainWindow.IsEnabled, "Bulk copy did not close.");
        }
    }

    /// <summary>
    /// The apply sheet as an operator sees it after double-clicking a printer
    /// file: header, what the file carries, and the plan as steps, waiting
    /// for the one confirmation.
    /// </summary>
    private static void CaptureApplySheet(string printerFile, string images)
    {
        using var fixture = new AppFixture(profilesDirectory: DemoSetups, args: printerFile);
        fixture.GoTo(AppFixture.Review);
        FunctionalTests.WaitUntil(() => fixture.MainWindow.FindAllDescendants(cf => cf.ByControlType(FlaUI.Core.Definitions.ControlType.Text))
            .Any(label => label.Name.Contains("Create printer", StringComparison.Ordinal) && !label.IsOffscreen),
            "The sheet never showed its steps.", 60_000);
        Thread.Sleep(500);
        fixture.MainWindow.CaptureToFile(Path.Combine(images, "gui-review-and-apply.png"));
    }

    /// <summary>
    /// The Intune wizard is a modal dialog opened from a button on the Tools
    /// tab, not a tab of its own, so the loop above never sees it. Captures
    /// both of its pages using the same profile/binary the wizard's own
    /// happy-path functional test drives, so the images shown on the product
    /// site match a package that actually validates, not a mockup.
    /// </summary>
    private static void CaptureIntuneWizard(AppFixture fixture, string images)
    {
        var profilePath = Path.Combine(DemoSetups, "accounting.ssb");
        var outputDir = Path.Combine(DemoRoot, "intune-" + DateTime.Now.ToString("yyyyMMdd-HHmmss"));
        fixture.GoTo("Intune package");
        FunctionalTests.FindButton(fixture.MainWindow, "Build an Intune printer app...").Invoke();
        Window? dialog = null;
        FunctionalTests.WaitUntil(() => (dialog = fixture.MainWindow.ModalWindows
            .Concat(fixture.App.GetAllTopLevelWindows(fixture.Automation))
            .FirstOrDefault(w => w.Title == "Build an Intune printer app")) != null,
            "Intune wizard did not open.");
        try
        {
            FunctionalTests.FindVisibleIntuneControl(dialog!, "intune-profile");
            FunctionalTests.SetText(FunctionalTests.Find(dialog!, "intune-profile").AsTextBox(), profilePath);
            FunctionalTests.SetText(FunctionalTests.Find(dialog!, "intune-binary").AsTextBox(), DemoCli);
            // The bundled example profile carries no local driver archive.
            FunctionalTests.Find(dialog!, "Driver is managed separately and will be registered before installation").AsCheckBox().Click();
            FunctionalTests.WaitForText(FunctionalTests.Find(dialog!, "intune-display-name").AsTextBox(),
                t => t == "Example — Accounting Copier");
            FunctionalTests.SetText(FunctionalTests.Find(dialog!, "intune-output").AsTextBox(), outputDir);
            Thread.Sleep(300);
            dialog!.CaptureToFile(Path.Combine(images, "gui-intune-wizard.png"));

            FunctionalTests.FindButton(dialog!, "Validate and preview package").Invoke();
            FunctionalTests.WaitForText(FunctionalTests.FindVisibleIntuneControl(dialog!, "intune-preview").AsTextBox(),
                t => t.Contains("example-accounting-copier-", StringComparison.Ordinal));
            Thread.Sleep(300);
            dialog!.CaptureToFile(Path.Combine(images, "gui-intune-review.png"));
        }
        finally
        {
            try { if (Directory.Exists(outputDir)) Directory.Delete(outputDir, recursive: true); } catch { /* best-effort cleanup */ }
        }
    }
}
