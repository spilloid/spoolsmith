using System;
using System.IO;
using System.Threading;
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
    }
}
