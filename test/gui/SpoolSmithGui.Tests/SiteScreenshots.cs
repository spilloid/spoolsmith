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
        using var fixture = new AppFixture();
        var images = Path.Combine(fixture.RepoRoot, "docs", "img");
        Directory.CreateDirectory(images);
        foreach (var (tab, file) in new[] {
            ("This PC", "gui-this-pc.png"),
            ("Add a printer", "gui-add-printer.png"),
            ("Review and apply", "gui-review-and-apply.png"),
            ("Inspect", "gui-tools.png") })
        {
            fixture.SelectTab(tab);
            Thread.Sleep(500);
            fixture.MainWindow.CaptureToFile(Path.Combine(images, file));
        }
        // Before CaptureIntuneWizard, which is written to be the last step
        // in this method: it never closes its own modal dialog, so anything
        // run after it would fight that leftover dialog for the main window.
        CaptureCopyAllDialog(fixture, images);
        CaptureIntuneWizard(fixture, images);
    }

    /// <summary>
    /// "Copy all printers..." opens a modal dialog from This PC, not a tab of
    /// its own, so the loop above never sees it. Skips silently if this
    /// machine has no printer queues at all (the button stays disabled) --
    /// every real capture host is expected to have at least the built-in
    /// Microsoft virtual printers, so this is a defensive no-op, not the
    /// normal path.
    /// </summary>
    private static void CaptureCopyAllDialog(AppFixture fixture, string images)
    {
        fixture.SelectTab("This PC");
        var copyAll = FunctionalTests.FindButton(fixture.MainWindow, "Copy all printers...");
        FunctionalTests.WaitUntil(() => FunctionalTests.FindButton(fixture.MainWindow, "Refresh").IsEnabled,
            "Printer inventory did not finish loading.");
        if (!copyAll.IsEnabled) return;
        copyAll.Invoke();
        Window? dialog = null;
        FunctionalTests.WaitUntil(() => (dialog = fixture.MainWindow.ModalWindows
            .Concat(fixture.App.GetAllTopLevelWindows(fixture.Automation))
            .FirstOrDefault(w => w.Title == "Copy all printers")) != null,
            "Bulk copy did not open.");
        try
        {
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
    /// The Intune wizard is a modal dialog opened from a button on the Tools
    /// tab, not a tab of its own, so the loop above never sees it. Captures
    /// both of its pages using the same profile/binary the wizard's own
    /// happy-path functional test drives, so the images shown on the product
    /// site match a package that actually validates, not a mockup.
    /// </summary>
    private static void CaptureIntuneWizard(AppFixture fixture, string images)
    {
        var profilePath = Path.Combine(fixture.RepoRoot, "examples", "intune", "accounting.json");
        var outputDir = Path.Combine(Path.GetTempPath(), "spoolsmith-intune-screenshot-" + Guid.NewGuid().ToString("N"));
        fixture.SelectTab("Tools");
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
            FunctionalTests.SetText(FunctionalTests.Find(dialog!, "intune-binary").AsTextBox(), fixture.CliExePath);
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
